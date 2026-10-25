//go:build integration

// Package load_test 覆盖「名额释放任务 vs 支付回调」的竞态场景。
//
// 场景与状态机的对应关系（docs/state-machine.md、docs/constraints.md）：
//   - 订单进入 pending_payment 时，其 lock 已经 active → consumed，
//     且 active → consumed 与 INSERT enrollment 在同一事务内完成，
//     所以 lock 过期任务（WHERE status='active'）不会扫到该订单对应的 lock；
//   - 真正会把 pending_payment 订单的名额回补、并与支付回调竞争的是
//     支付超时任务（WHERE status='pending_payment'，见 constraints.md 第 12 条）。
//
// 每轮同时启动三个 goroutine：lock 过期任务、支付超时任务、HandleNotify，校验：
//   - Order 只会落到 paid 或 expired（Enrollment 对应 paid / cancelled），
//     paid_at 与 expired_at 不会同时存在（不存在「既 paid 又 expired」）；
//   - paid：Redis remaining 保持消费后的值（0），回调不扣减/不回补；
//   - expired：Redis remaining 恰好回补 +1（到 1），不会回补两次；
//   - 对账等式 remaining + active locks + pending + paid/confirmed = total_slots 成立。
//
// 两个用例：
//   - TestRaceReleaseVsNotify：50 轮完全同时起跑的真实竞争；
//   - TestReleaseVsNotifyBothTerminalStates：用确定的先后顺序分别让
//     支付超时 / 回调先到，保证 paid 与 expired 两条终态都被覆盖
//     （并发竞争里 notify 通常更快，只跑前者会导致 expired 分支零覆盖）。
//
// 运行方式：
//
//	go test -tags=integration ./tests/load -run 'TestRaceReleaseVsNotify|TestReleaseVsNotifyBothTerminalStates' -v -count=1
package load_test

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"bike-enroll/server/internal/config"
	"bike-enroll/server/internal/job"
	"bike-enroll/server/internal/model"
	paymentpkg "bike-enroll/server/internal/pkg/payment"
	"bike-enroll/server/internal/repository"
	paymentrepo "bike-enroll/server/internal/repository/payment"
	paymentsvc "bike-enroll/server/internal/service/payment"
)

const (
	raceIterations = 50
	raceTotalSlots = 1
	racePrice      = 100 // 分
	payTimeout     = 15 * time.Minute
	maxLockRetries = 5
	// 确定性顺序用例中，让另一方"晚到"的时长（远大于单次任务耗时）。
	headStart = 80 * time.Millisecond
)

// raceEnv 是本用例依赖的数据库/Redis 与固定夹具。
type raceEnv struct {
	db      *gorm.DB
	rdb     *redis.Client
	eventID string
	groupID string
}

// raceDeps 是被测的支付服务与两个释放任务。
type raceDeps struct {
	payments   *paymentsvc.Service
	payTimeout *job.PayTimeoutJob
	lockExpiry *job.LockExpiryJob
}

// raceFixture 描述一笔 pending_payment 订单及其已消费的 lock。
type raceFixture struct {
	orderID      string
	enrollmentID string
	lockToken    string
}

// groupOccupancy 是对账等式需要的三项计数。
type groupOccupancy struct {
	activeLocks int
	pending     int
	paid        int
}

// roundResult 是一轮竞争的观测结果。
//
// backfill = 竞争结束后的 Redis remaining − 竞争开始前的 remaining，
// 只允许 0（回调胜出）或 1（释放胜出，恰好回补一次）。
type roundResult struct {
	status   string
	backfill int
	skip     bool
}

func setupRaceEnv(t *testing.T) (*raceEnv, *raceDeps) {
	t.Helper()

	cfg := config.Load()
	db, err := repository.OpenMySQL(cfg.MySQL)
	if err != nil {
		t.Fatalf("连接 MySQL 失败: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("获取 sql.DB 失败: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	rdb, err := repository.OpenRedis(cfg.Redis)
	if err != nil {
		t.Fatalf("连接 Redis 失败: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })

	now := time.Now()
	env := &raceEnv{
		db:      db,
		rdb:     rdb,
		eventID: uuid.NewString(),
		groupID: uuid.NewString(),
	}

	event := &model.Event{
		ID: env.eventID, Title: "竞态压测赛事",
		StartTime: now.Add(24 * time.Hour), EndTime: now.Add(26 * time.Hour),
		Location: "上海", LifecycleStatus: model.LifecyclePublished, Description: "release vs notify",
		Rules: "", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(event).Error; err != nil {
		t.Fatalf("创建赛事失败: %v", err)
	}
	group := &model.Group{
		ID: env.groupID, EventID: env.eventID, Name: "竞态组", Price: racePrice,
		TotalSlots: raceTotalSlots, StartTime: now.Add(24 * time.Hour), Status: "open",
		FormSchema: map[string]any{}, DisclaimerVersion: "v1",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(group).Error; err != nil {
		t.Fatalf("创建组别失败: %v", err)
	}

	// 测试开始前清掉可能残留的任务单实例锁（TTL 60s），避免整个用例被跳过。
	if err := rdb.Del(context.Background(), jobLockKeys()...).Err(); err != nil {
		t.Fatalf("清理任务锁失败: %v", err)
	}

	t.Cleanup(func() {
		ctx := context.Background()
		env.db.Where("group_id = ?", env.groupID).Delete(&model.EnrollmentLock{})
		env.db.Where("group_id = ?", env.groupID).Delete(&model.Enrollment{})
		env.db.Where("group_id = ?", env.groupID).Delete(&model.Order{})
		env.db.Where("id = ?", env.groupID).Delete(&model.Group{})
		env.db.Where("id = ?", env.eventID).Delete(&model.Event{})
		env.rdb.Del(ctx, env.remainingKey())
		env.rdb.Del(ctx, jobLockKeys()...)
	})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps := &raceDeps{
		payments:   paymentsvc.New(paymentrepo.NewGORM(db), logger),
		payTimeout: job.NewPayTimeoutJob(db, rdb, payTimeout, logger),
		lockExpiry: job.NewLockExpiryJob(db, rdb, logger),
	}
	return env, deps
}

func jobLockKeys() []string {
	return []string{"job:lock-expiry:lock", "job:pay-timeout:lock"}
}

func (env *raceEnv) remainingKey() string {
	return "group:" + env.groupID + ":remaining"
}

// reset 把组别恢复到「名额未占用」的干净状态。
func (env *raceEnv) reset(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	if err := env.db.Where("group_id = ?", env.groupID).Delete(&model.EnrollmentLock{}).Error; err != nil {
		t.Fatalf("清理 lock 失败: %v", err)
	}
	if err := env.db.Where("group_id = ?", env.groupID).Delete(&model.Enrollment{}).Error; err != nil {
		t.Fatalf("清理 enrollment 失败: %v", err)
	}
	if err := env.db.Where("group_id = ?", env.groupID).Delete(&model.Order{}).Error; err != nil {
		t.Fatalf("清理 order 失败: %v", err)
	}
	if err := env.rdb.Set(ctx, env.remainingKey(), raceTotalSlots, 0).Err(); err != nil {
		t.Fatalf("重置 Redis 名额失败: %v", err)
	}
}

// seedPendingOrder 创建一笔 pending_payment 订单：
// lock 状态为 consumed（active → consumed 与创建 enrollment 同一事务），
// expires_at 设为 1 秒后（用于验证 lock 过期任务不会回收已消费的锁），
// order.created_at 回拨 30 分钟，保证支付超时任务能扫到它。
func (env *raceEnv) seedPendingOrder(t *testing.T) raceFixture {
	t.Helper()
	ctx := context.Background()
	now := time.Now()

	fixture := raceFixture{
		orderID:      strings.ReplaceAll(uuid.NewString(), "-", ""),
		enrollmentID: uuid.NewString(),
		lockToken:    uuid.NewString(),
	}
	userID := uuid.NewString()
	consumedAt := now

	lock := &model.EnrollmentLock{
		Token: fixture.lockToken, UserID: userID, GroupID: env.groupID,
		Status: "consumed", ExpiresAt: now.Add(time.Second), ConsumedAt: &consumedAt,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := env.db.Create(lock).Error; err != nil {
		t.Fatalf("创建 lock 失败: %v", err)
	}

	enrollment := &model.Enrollment{
		ID: fixture.enrollmentID, UserID: userID, EventID: env.eventID,
		GroupID: env.groupID, OrderID: fixture.orderID, Status: "pending_payment",
		FormData: map[string]any{"name": "race"}, CreatedAt: now, UpdatedAt: now,
	}
	if err := env.db.Create(enrollment).Error; err != nil {
		t.Fatalf("创建 enrollment 失败: %v", err)
	}

	order := &model.Order{
		ID: fixture.orderID, EnrollmentID: fixture.enrollmentID, UserID: userID,
		GroupID: env.groupID, Amount: racePrice, Status: "pending_payment",
		CreatedAt: now.Add(-30 * time.Minute), UpdatedAt: now.Add(-30 * time.Minute),
	}
	if err := env.db.Create(order).Error; err != nil {
		t.Fatalf("创建 order 失败: %v", err)
	}

	// 名额已被 lock 阶段消费：remaining = total - 1。
	if err := env.rdb.Set(ctx, env.remainingKey(), raceTotalSlots-1, 0).Err(); err != nil {
		t.Fatalf("消费 Redis 名额失败: %v", err)
	}
	return fixture
}

// occupancy 统计对账等式所需的三项计数（全部实时读 MySQL）。
func (env *raceEnv) occupancy(t *testing.T) groupOccupancy {
	t.Helper()

	var active, pending, paid int64
	if err := env.db.Model(&model.EnrollmentLock{}).
		Where("group_id = ? AND status = ?", env.groupID, "active").
		Count(&active).Error; err != nil {
		t.Fatalf("统计 active lock 失败: %v", err)
	}
	if err := env.db.Model(&model.Enrollment{}).
		Where("group_id = ? AND status = ?", env.groupID, "pending_payment").
		Count(&pending).Error; err != nil {
		t.Fatalf("统计待支付报名失败: %v", err)
	}
	if err := env.db.Model(&model.Enrollment{}).
		Where("group_id = ? AND status IN ?", env.groupID, []string{"paid", "confirmed"}).
		Count(&paid).Error; err != nil {
		t.Fatalf("统计已支付报名失败: %v", err)
	}
	return groupOccupancy{activeLocks: int(active), pending: int(pending), paid: int(paid)}
}

func raceNotifyResult(orderID string) *paymentpkg.NotifyResult {
	return &paymentpkg.NotifyResult{
		EventType:     "TRANSACTION.SUCCESS",
		OutTradeNo:    orderID,
		TransactionID: "race-" + uuid.NewString(),
		TradeState:    "SUCCESS",
		Amount:        paymentpkg.NotifyAmount{Total: racePrice, Currency: "CNY"},
	}
}

// runRaceRound 跑一轮竞争并校验终态。
//
// notifyDelay / jobDelay 用于构造确定的先后顺序（两者都为 0 时是完全同时起跑）。
// 返回 skip=true 表示任务单实例锁被线上调度器短暂持有，本轮需重试。
func (env *raceEnv) runRaceRound(
	t *testing.T,
	deps *raceDeps,
	label string,
	notifyDelay, jobDelay time.Duration,
) (result roundResult) {
	t.Helper()

	env.reset(t)
	fixture := env.seedPendingOrder(t)

	var (
		wg                             sync.WaitGroup
		lockErr, timeoutErr, notifyErr error
		lockResult                     job.LockExpiryResult
		timeoutResult                  job.PayTimeoutResult
	)

	start := make(chan struct{})
	wg.Add(3)
	go func() {
		defer wg.Done()
		<-start
		lockResult, lockErr = deps.lockExpiry.Run(context.Background())
	}()
	go func() {
		defer wg.Done()
		<-start
		if jobDelay > 0 {
			time.Sleep(jobDelay)
		}
		timeoutResult, timeoutErr = deps.payTimeout.Run(context.Background())
	}()
	go func() {
		defer wg.Done()
		<-start
		if notifyDelay > 0 {
			time.Sleep(notifyDelay)
		}
		notifyErr = deps.payments.HandleNotify(context.Background(), raceNotifyResult(fixture.orderID))
	}()
	close(start)
	wg.Wait()

	if lockErr != nil {
		t.Fatalf("%s: lock 过期任务失败: %v", label, lockErr)
	}
	if timeoutErr != nil {
		t.Fatalf("%s: 支付超时任务失败: %v", label, timeoutErr)
	}
	if notifyErr != nil {
		t.Fatalf("%s: 支付回调失败: %v", label, notifyErr)
	}
	if timeoutResult.Locked {
		return roundResult{skip: true}
	}

	var order model.Order
	var enrollment model.Enrollment
	var lock model.EnrollmentLock
	if err := env.db.Where("id = ?", fixture.orderID).First(&order).Error; err != nil {
		t.Fatalf("%s: 读取 Order 失败: %v", label, err)
	}
	if err := env.db.Where("id = ?", fixture.enrollmentID).First(&enrollment).Error; err != nil {
		t.Fatalf("%s: 读取 Enrollment 失败: %v", label, err)
	}
	if err := env.db.Where("token = ?", fixture.lockToken).First(&lock).Error; err != nil {
		t.Fatalf("%s: 读取 lock 失败: %v", label, err)
	}

	remainingBefore := raceTotalSlots - 1
	remaining, err := env.rdb.Get(context.Background(), env.remainingKey()).Int()
	if err != nil {
		t.Fatalf("%s: 读取 Redis remaining 失败: %v", label, err)
	}
	backfill := remaining - remainingBefore
	if backfill != 0 && backfill != 1 {
		t.Fatalf("%s: Redis 回补次数 = %d，只允许 0 或 1（remaining: %d -> %d）",
			label, backfill, remainingBefore, remaining)
	}
	counts := env.occupancy(t)

	// 1) lock 过期任务不得回收已消费的锁。
	if lock.Status != "consumed" || lock.ExpiredAt != nil {
		t.Fatalf("%s: lock 被错误回收: status=%s expired_at=%v（lockResult.Reclaimed=%d）",
			label, lock.Status, lock.ExpiredAt, lockResult.Reclaimed)
	}

	// 2) Order 只能落到 paid 或 expired，且 paid_at / expired_at 互斥。
	if order.PaidAt != nil && order.ExpiredAt != nil {
		t.Fatalf("%s: Order 同时存在 paid_at 与 expired_at: %+v", label, order)
	}
	if order.PaidAt == nil && order.ExpiredAt == nil {
		t.Fatalf("%s: Order 既未 paid 也未 expired: %+v", label, order)
	}

	switch order.Status {
	case "paid":
		if order.PaidAt == nil || order.TransactionID == nil {
			t.Fatalf("%s: Order paid 但缺少 paid_at/transaction_id: %+v", label, order)
		}
		if enrollment.Status != "paid" || enrollment.PaidAt == nil {
			t.Fatalf("%s: Order paid 但 Enrollment 状态为 %s", label, enrollment.Status)
		}
		if enrollment.CancelledAt != nil {
			t.Fatalf("%s: Enrollment 同时存在 paid_at 与 cancelled_at", label)
		}
		if backfill != 0 {
			t.Fatalf("%s: 回调胜出，Redis 应回补 0 次（remaining 保持 %d），实际回补 %d 次",
				label, raceTotalSlots-1, backfill)
		}
		if timeoutResult.Expired != 0 {
			t.Fatalf("%s: 回调胜出，超时任务却回补了 %d 次", label, timeoutResult.Expired)
		}

	case "expired":
		if order.TransactionID != nil {
			t.Fatalf("%s: Order expired 却写入了 transaction_id", label)
		}
		if enrollment.Status != "cancelled" || enrollment.CancelledAt == nil {
			t.Fatalf("%s: Order expired 但 Enrollment 状态为 %s", label, enrollment.Status)
		}
		if enrollment.PaidAt != nil {
			t.Fatalf("%s: Enrollment 同时存在 paid_at 与 cancelled_at", label)
		}
		if backfill != 1 {
			t.Fatalf("%s: 超时胜出，Redis 应恰好回补 1 次（remaining %d -> %d），实际回补 %d 次",
				label, raceTotalSlots-1, raceTotalSlots, backfill)
		}
		// 线上调度器也可能先一步完成超时处理，所以这里只断言「回补恰好一次」
		// 由 remaining 体现，不强行要求本进程的任务 Expired==1。
		if timeoutResult.Expired > 1 {
			t.Fatalf("%s: 超时任务回补了 %d 次（应为 0 或 1）", label, timeoutResult.Expired)
		}

	default:
		t.Fatalf("%s: Order 终态非法: %s", label, order.Status)
	}

	// 3) 对账等式：remaining + active locks + pending + paid/confirmed = total_slots。
	sum := remaining + counts.activeLocks + counts.pending + counts.paid
	if sum != raceTotalSlots {
		t.Fatalf("%s: 对账等式不成立: remaining=%d + active=%d + pending=%d + paid=%d = %d，期望 %d",
			label, remaining, counts.activeLocks, counts.pending, counts.paid, sum, raceTotalSlots)
	}
	if counts.activeLocks != 0 {
		t.Fatalf("%s: 不应存在 active lock，实际 %d", label, counts.activeLocks)
	}

	return roundResult{status: order.Status, backfill: backfill}
}

// runRoundWithRetry 处理任务单实例锁被线上调度器短暂持有的情况。
func runRoundWithRetry(
	t *testing.T,
	env *raceEnv,
	deps *raceDeps,
	label string,
	notifyDelay, jobDelay time.Duration,
) roundResult {
	t.Helper()
	for attempt := 1; attempt <= maxLockRetries; attempt++ {
		result := env.runRaceRound(t, deps, label, notifyDelay, jobDelay)
		if result.skip {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		return result
	}
	t.Fatalf("%s: 连续 %d 次未拿到任务单实例锁，无法完成竞争", label, maxLockRetries)
	return roundResult{}
}

// TestRaceReleaseVsNotify 执行 50 轮完全同时起跑的「释放 vs 回调」竞争。
//
// 逐轮记录终态与 Redis 回补次数，最后汇总断言：
//   - 终态只落在 {paid, expired+cancelled}；
//   - 回补次数只落在 {0, 1}，且 paid↔0、expired↔1；
//   - 对账等式每轮成立。
func TestRaceReleaseVsNotify(t *testing.T) {
	env, deps := setupRaceEnv(t)

	var (
		paid, expired    int
		backfillZero     int
		backfillOne      int
		backfillIllegal  int
		illegalTerminals int
	)

	for round := 1; round <= raceIterations; round++ {
		label := "第 " + strconv.Itoa(round) + " 轮"
		result := runRoundWithRetry(t, env, deps, label, 0, 0)

		switch result.status {
		case "paid":
			paid++
		case "expired":
			expired++
		default:
			illegalTerminals++
		}
		switch result.backfill {
		case 0:
			backfillZero++
		case 1:
			backfillOne++
		default:
			backfillIllegal++
		}
	}

	if illegalTerminals != 0 {
		t.Fatalf("%d 轮出现非法终态", illegalTerminals)
	}
	if backfillIllegal != 0 {
		t.Fatalf("%d 轮出现非法回补次数（只允许 0 或 1）", backfillIllegal)
	}
	if paid+expired != raceIterations {
		t.Fatalf("终态合计 = %d，期望 %d", paid+expired, raceIterations)
	}
	if backfillZero+backfillOne != raceIterations {
		t.Fatalf("回补次数合计 = %d，期望 %d", backfillZero+backfillOne, raceIterations)
	}
	if paid != backfillZero || expired != backfillOne {
		t.Fatalf("终态与回补次数不一致：paid=%d/backfill0=%d，expired=%d/backfill1=%d",
			paid, backfillZero, expired, backfillOne)
	}

	t.Logf("%d 轮同时起跑：paid=%d（回补 0 次），expired+cancelled=%d（回补 1 次）",
		raceIterations, paid, expired)
	t.Logf("回补次数分布：0 次=%d，1 次=%d，>1 次或负数=0；非法终态=0；对账等式每轮成立",
		backfillZero, backfillOne)
}

// TestReleaseVsNotifyBothTerminalStates 用确定的先后顺序覆盖两条终态。
//
// 并发竞争里 HandleNotify 的单行更新通常快于任务的「扫描 + 行锁 + 条件更新」，
// 只跑同时起跑会长期只命中 paid 分支；这里分别让支付超时任务 / 回调先到，
// 保证 expired+cancelled 分支同样被验证（remaining 恰好 +1、对账等式成立）。
func TestReleaseVsNotifyBothTerminalStates(t *testing.T) {
	env, deps := setupRaceEnv(t)

	cases := []struct {
		name        string
		notifyDelay time.Duration
		jobDelay    time.Duration
		want        string
	}{
		{name: "支付超时先到", notifyDelay: headStart, jobDelay: 0, want: "expired"},
		{name: "回调先到", notifyDelay: 0, jobDelay: headStart, want: "paid"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			const rounds = raceIterations
			wantBackfill := 0
			if tc.want == "expired" {
				wantBackfill = 1
			}

			var (
				matched     int
				backfillBad int
			)
			for round := 1; round <= rounds; round++ {
				label := tc.name + " / 第 " + strconv.Itoa(round) + " 轮"
				result := runRoundWithRetry(t, env, deps, label, tc.notifyDelay, tc.jobDelay)
				if result.status != tc.want {
					t.Fatalf("%s: 终态 = %s，期望 %s", label, result.status, tc.want)
				}
				matched++
				if result.backfill != wantBackfill {
					backfillBad++
					t.Fatalf("%s: Redis 回补次数 = %d，期望 %d", label, result.backfill, wantBackfill)
				}
			}
			if matched != rounds || backfillBad != 0 {
				t.Fatalf("%s：有效轮次=%d，回补次数异常轮次=%d", tc.name, matched, backfillBad)
			}
			t.Logf("%s：%d 轮终态均为 %s，Redis 回补次数均为 %d", tc.name, rounds, tc.want, wantBackfill)
		})
	}
}
