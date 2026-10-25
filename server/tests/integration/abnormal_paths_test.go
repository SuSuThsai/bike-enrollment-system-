//go:build integration

// Package integration_test 提供里程碑三第五步的异常路径集成测试。
//
// 运行前需确保本地 docker compose 的 MySQL/Redis 可用，且已执行 server/migrations 下的迁移。
//
// 运行方式（每次只跑一个）：
//
//	go test -tags=integration ./tests/integration -run '^TestA_' -v -count=1
//	go test -tags=integration ./tests/integration -run '^TestB_' -v -count=1
//	go test -tags=integration ./tests/integration -run '^TestC_' -v -count=1
//	go test -tags=integration ./tests/integration -run '^TestD_' -v -count=1
//
// 测试直接调用 service / job 层，MySQL 与 Redis 使用真实实例；
// 支付回调直接构造 Provider 已验证/解析后的 NotifyResult，再交给 Service.HandleNotify；
// 签名与解密链路由 pkg/payment 与 pkg/wechat/pay 的单元测试覆盖。
package integration_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
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
	enrollmentrepo "bike-enroll/server/internal/repository/enrollment"
	lockrepo "bike-enroll/server/internal/repository/lock"
	paymentrepo "bike-enroll/server/internal/repository/payment"
	enrollmentsvc "bike-enroll/server/internal/service/enrollment"
	paymentsvc "bike-enroll/server/internal/service/payment"
)

type testEnv struct {
	db  *gorm.DB
	rdb *redis.Client
}

func newEnv(t *testing.T) *testEnv {
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

	return &testEnv{db: db, rdb: rdb}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newEnrollmentService(env *testEnv) *enrollmentsvc.Service {
	return enrollmentsvc.New(
		lockrepo.NewGORM(env.db),
		lockrepo.NewRedisStore(env.rdb),
		enrollmentrepo.NewGORM(env.db),
		15*time.Minute,
		testLogger(),
	)
}

// seedGroup 创建一场开放报名的赛事与一个组别，并把 Redis 名额初始化为 totalSlots。
func seedGroup(t *testing.T, env *testEnv, totalSlots int) (eventID, groupID string) {
	t.Helper()

	now := time.Now()
	eventID = uuid.NewString()
	groupID = uuid.NewString()

	event := &model.Event{
		ID:              eventID,
		Title:           "M3S5 异常路径测试赛事",
		CoverURL:        "",
		StartTime:       now.Add(24 * time.Hour),
		EndTime:         now.Add(26 * time.Hour),
		Location:        "测试地点",
		LifecycleStatus: model.LifecyclePublished,
		Description:     "集成测试",
		Rules:           "",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := env.db.Create(event).Error; err != nil {
		t.Fatalf("创建赛事失败: %v", err)
	}

	group := &model.Group{
		ID:                groupID,
		EventID:           eventID,
		Name:              "异常路径测试组",
		Price:             10000,
		TotalSlots:        totalSlots,
		StartTime:         now.Add(24 * time.Hour),
		Status:            "open",
		FormSchema:        map[string]any{"fields": []any{}},
		DisclaimerVersion: "v1",
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := env.db.Create(group).Error; err != nil {
		t.Fatalf("创建组别失败: %v", err)
	}

	key := "group:" + groupID + ":remaining"
	if err := env.rdb.Set(context.Background(), key, totalSlots, 0).Err(); err != nil {
		t.Fatalf("初始化 Redis 名额失败: %v", err)
	}

	t.Cleanup(func() {
		env.db.Where("group_id = ?", groupID).Delete(&model.DisclaimerSignature{})
		env.db.Where("group_id = ?", groupID).Delete(&model.Order{})
		env.db.Where("group_id = ?", groupID).Delete(&model.Enrollment{})
		env.db.Where("group_id = ?", groupID).Delete(&model.EnrollmentLock{})
		env.db.Where("event_id = ?", eventID).Delete(&model.Group{})
		env.db.Where("id = ?", eventID).Delete(&model.Event{})
		env.rdb.Del(context.Background(), key)
	})

	return eventID, groupID
}

// seedPendingOrder 创建一笔 pending_payment 订单及其报名，Redis 名额置为 4。
func seedPendingOrder(t *testing.T, env *testEnv, createdAt time.Time, amount int) (orderID, enrollmentID, groupID string) {
	t.Helper()

	userID := uuid.NewString()
	eventID := uuid.NewString()
	groupID = uuid.NewString()
	enrollmentID = uuid.NewString()
	orderID = uuid.NewString()

	enrollment := &model.Enrollment{
		ID:        enrollmentID,
		UserID:    userID,
		EventID:   eventID,
		GroupID:   groupID,
		OrderID:   orderID,
		Status:    "pending_payment",
		FormData:  map[string]any{"name": "异常路径测试"},
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
	if err := env.db.Create(enrollment).Error; err != nil {
		t.Fatalf("创建 Enrollment 失败: %v", err)
	}

	order := &model.Order{
		ID:           orderID,
		EnrollmentID: enrollmentID,
		UserID:       userID,
		GroupID:      groupID,
		Amount:       amount,
		Status:       "pending_payment",
		CreatedAt:    createdAt,
		UpdatedAt:    createdAt,
	}
	if err := env.db.Create(order).Error; err != nil {
		t.Fatalf("创建 Order 失败: %v", err)
	}

	key := "group:" + groupID + ":remaining"
	if err := env.rdb.Set(context.Background(), key, 4, 0).Err(); err != nil {
		t.Fatalf("初始化 Redis 名额失败: %v", err)
	}

	t.Cleanup(func() {
		env.db.Where("id = ?", orderID).Delete(&model.Order{})
		env.db.Where("id = ?", enrollmentID).Delete(&model.Enrollment{})
		env.rdb.Del(context.Background(), key)
	})

	return orderID, enrollmentID, groupID
}

func redisInt(t *testing.T, env *testEnv, key string) int {
	t.Helper()
	value, err := env.rdb.Get(context.Background(), key).Int()
	if err != nil {
		t.Fatalf("读取 Redis %s 失败: %v", key, err)
	}
	return value
}

func activeLockCount(t *testing.T, env *testEnv, groupID string) int64 {
	t.Helper()
	var count int64
	if err := env.db.Model(&model.EnrollmentLock{}).
		Where("group_id = ? AND status = ?", groupID, "active").
		Count(&count).Error; err != nil {
		t.Fatalf("统计 active lock 失败: %v", err)
	}
	return count
}

func formatTime(value *time.Time) string {
	if value == nil {
		return "NULL"
	}
	return value.Format("2006-01-02T15:04:05.000Z07:00")
}

func loadOrder(t *testing.T, env *testEnv, orderID string) model.Order {
	t.Helper()
	var order model.Order
	if err := env.db.Where("id = ?", orderID).First(&order).Error; err != nil {
		t.Fatalf("读取 Order %s 失败: %v", orderID, err)
	}
	return order
}

func loadEnrollment(t *testing.T, env *testEnv, enrollmentID string) model.Enrollment {
	t.Helper()
	var enrollment model.Enrollment
	if err := env.db.Where("id = ?", enrollmentID).First(&enrollment).Error; err != nil {
		t.Fatalf("读取 Enrollment %s 失败: %v", enrollmentID, err)
	}
	return enrollment
}

func newNotifyResult(orderID, transactionID string, amount int) *paymentpkg.NotifyResult {
	return &paymentpkg.NotifyResult{
		EventType:     "TRANSACTION.SUCCESS",
		OutTradeNo:    orderID,
		TransactionID: transactionID,
		TradeState:    "SUCCESS",
		Amount:        paymentpkg.NotifyAmount{Total: amount, Currency: "CNY"},
	}
}

// TestA_LockExpiryReclaimsRedis 验证：active lock 过期后由任务回补 Redis，且不残留 active 记录。
func TestA_LockExpiryReclaimsRedis(t *testing.T) {
	env := newEnv(t)
	_, groupID := seedGroup(t, env, 5)
	service := newEnrollmentService(env)
	ctx := context.Background()

	userID := uuid.NewString()
	lock, err := service.Lock(ctx, userID, groupID)
	if err != nil {
		t.Fatalf("锁定名额失败: %v", err)
	}
	t.Cleanup(func() {
		env.rdb.Del(context.Background(), "lock:"+userID+":"+groupID, "lock:"+lock.Token)
	})

	before := redisInt(t, env, "group:"+groupID+":remaining")
	activeBefore := activeLockCount(t, env, groupID)
	if before != 4 {
		t.Fatalf("锁定后 Redis remaining = %d，期望 4", before)
	}
	if activeBefore != 1 {
		t.Fatalf("锁定后 active lock 数 = %d，期望 1", activeBefore)
	}

	if err := env.db.Exec(
		"UPDATE enrollment_locks SET expires_at = NOW(3) - INTERVAL 1 MINUTE WHERE token = ?",
		lock.Token,
	).Error; err != nil {
		t.Fatalf("把 expires_at 改为过去时间失败: %v", err)
	}

	var lockBefore model.EnrollmentLock
	if err := env.db.Where("token = ?", lock.Token).First(&lockBefore).Error; err != nil {
		t.Fatalf("读取过期前 lock 失败: %v", err)
	}
	if !lockBefore.ExpiresAt.Before(time.Now()) {
		t.Fatalf("lock.expires_at = %s，未被改为过去时间", lockBefore.ExpiresAt)
	}

	runner := job.NewLockExpiryJob(env.db, env.rdb, testLogger())
	result, err := runner.Run(ctx)
	if err != nil {
		t.Fatalf("执行 lock 过期任务失败: %v", err)
	}
	if result.Locked {
		t.Fatalf("lock 过期任务被单实例锁跳过，测试环境存在并发任务")
	}

	after := redisInt(t, env, "group:"+groupID+":remaining")
	activeAfter := activeLockCount(t, env, groupID)
	var lockAfter model.EnrollmentLock
	if err := env.db.Where("token = ?", lock.Token).First(&lockAfter).Error; err != nil {
		t.Fatalf("读取过期后 lock 失败: %v", err)
	}

	t.Logf("A redis remaining: before=%d after=%d", before, after)
	t.Logf("A lock: token=%s status_before=%s status_after=%s expires_at=%s expired_at=%s",
		lock.Token, lockBefore.Status, lockAfter.Status, lockAfter.ExpiresAt.Format(time.RFC3339), formatTime(lockAfter.ExpiredAt))
	t.Logf("A mysql active locks: before=%d after=%d; job reclaimed=%d", activeBefore, activeAfter, result.Reclaimed)

	if result.Reclaimed != 1 {
		t.Errorf("任务回补数 = %d，期望 1", result.Reclaimed)
	}
	if after != before+1 {
		t.Errorf("Redis 回补后 = %d，期望 %d", after, before+1)
	}
	if lockAfter.Status != "expired" {
		t.Errorf("lock.status = %q，期望 expired", lockAfter.Status)
	}
	if lockAfter.ExpiredAt == nil {
		t.Errorf("lock.expired_at 为空，期望有值")
	}
	if activeAfter != 0 {
		t.Errorf("MySQL active lock 数 = %d，期望 0", activeAfter)
	}
}

// TestB_DuplicatePaymentNotify 验证：同一 transaction_id 的重复回调幂等，只迁移一次且不碰 Redis。
func TestB_DuplicatePaymentNotify(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	orderID, enrollmentID, groupID := seedPendingOrder(t, env, time.Now(), 10000)

	transactionID := "wx-dup-" + uuid.NewString()
	service := paymentsvc.New(paymentrepo.NewGORM(env.db), testLogger())
	notify := newNotifyResult(orderID, transactionID, 10000)
	redisKey := "group:" + groupID + ":remaining"

	if err := service.HandleNotify(ctx, notify); err != nil {
		t.Fatalf("第一次支付回调失败: %v", err)
	}
	firstOrder := loadOrder(t, env, orderID)
	firstEnrollment := loadEnrollment(t, env, enrollmentID)
	redisAfterFirst := redisInt(t, env, redisKey)
	t.Logf("B callback#1: order.status=%s order.paid_at=%s order.transaction_id=%s enrollment.status=%s enrollment.paid_at=%s redis=%d",
		firstOrder.Status, formatTime(firstOrder.PaidAt), derefString(firstOrder.TransactionID),
		firstEnrollment.Status, formatTime(firstEnrollment.PaidAt), redisAfterFirst)

	if err := service.HandleNotify(ctx, notify); err != nil {
		t.Fatalf("第二次支付回调失败: %v", err)
	}
	secondOrder := loadOrder(t, env, orderID)
	secondEnrollment := loadEnrollment(t, env, enrollmentID)
	redisAfterSecond := redisInt(t, env, redisKey)
	t.Logf("B callback#2: order.status=%s order.paid_at=%s order.transaction_id=%s enrollment.status=%s enrollment.paid_at=%s redis=%d",
		secondOrder.Status, formatTime(secondOrder.PaidAt), derefString(secondOrder.TransactionID),
		secondEnrollment.Status, formatTime(secondEnrollment.PaidAt), redisAfterSecond)

	var enrollmentCount int64
	if err := env.db.Model(&model.Enrollment{}).Where("order_id = ?", orderID).Count(&enrollmentCount).Error; err != nil {
		t.Fatalf("统计 Enrollment 失败: %v", err)
	}
	t.Logf("B enrollments for order=%d", enrollmentCount)

	if firstOrder.Status != "paid" || firstEnrollment.Status != "paid" {
		t.Errorf("第一次回调后状态异常: order=%s enrollment=%s", firstOrder.Status, firstEnrollment.Status)
	}
	if firstOrder.PaidAt == nil || firstEnrollment.PaidAt == nil {
		t.Fatalf("第一次回调后 paid_at 为空")
	}
	if secondOrder.Status != "paid" || secondEnrollment.Status != "paid" {
		t.Errorf("第二次回调后状态异常: order=%s enrollment=%s", secondOrder.Status, secondEnrollment.Status)
	}
	if secondOrder.PaidAt == nil || !secondOrder.PaidAt.Equal(*firstOrder.PaidAt) {
		t.Errorf("重复回调改变了 Order.paid_at: %s -> %s", formatTime(firstOrder.PaidAt), formatTime(secondOrder.PaidAt))
	}
	if secondEnrollment.PaidAt == nil || !secondEnrollment.PaidAt.Equal(*firstEnrollment.PaidAt) {
		t.Errorf("重复回调改变了 Enrollment.paid_at: %s -> %s", formatTime(firstEnrollment.PaidAt), formatTime(secondEnrollment.PaidAt))
	}
	if secondOrder.TransactionID == nil || *secondOrder.TransactionID != transactionID {
		t.Errorf("order.transaction_id = %s，期望 %s", derefString(secondOrder.TransactionID), transactionID)
	}
	if !secondOrder.UpdatedAt.Equal(firstOrder.UpdatedAt) {
		t.Errorf("重复回调更新了 Order.updated_at: %s -> %s", firstOrder.UpdatedAt, secondOrder.UpdatedAt)
	}
	if redisAfterFirst != 4 || redisAfterSecond != 4 {
		t.Errorf("回调不应修改 Redis: first=%d second=%d，期望均为 4", redisAfterFirst, redisAfterSecond)
	}
	if enrollmentCount != 1 {
		t.Errorf("同一订单的 Enrollment 数 = %d，期望 1", enrollmentCount)
	}
}

// TestC_ConcurrentLock 验证：100 个用户并发抢 10 个名额不会超卖，Redis 与 MySQL 计数一致。
func TestC_ConcurrentLock(t *testing.T) {
	env := newEnv(t)
	_, groupID := seedGroup(t, env, 10)
	service := newEnrollmentService(env)

	const (
		users = 100
		slots = 10
	)
	userIDs := make([]string, 0, users)
	for i := 0; i < users; i++ {
		userIDs = append(userIDs, uuid.NewString())
	}
	t.Cleanup(func() {
		keys := make([]string, 0, len(userIDs))
		for _, userID := range userIDs {
			keys = append(keys, "lock:"+userID+":"+groupID)
		}
		env.rdb.Del(context.Background(), keys...)
	})

	type outcome struct {
		token string
		err   error
	}
	outcomes := make(chan outcome, users)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, userID := range userIDs {
		userID := userID
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := service.Lock(context.Background(), userID, groupID)
			item := outcome{err: err}
			if result != nil {
				item.token = result.Token
			}
			outcomes <- item
		}()
	}
	close(start)
	wg.Wait()
	close(outcomes)

	success := 0
	soldOut := 0
	var tokens []string
	var other []error
	for item := range outcomes {
		switch {
		case item.err == nil:
			success++
			tokens = append(tokens, item.token)
		case errors.Is(item.err, enrollmentsvc.ErrGroupSoldOut):
			soldOut++
		default:
			other = append(other, item.err)
		}
	}
	t.Cleanup(func() {
		keys := make([]string, 0, len(tokens))
		for _, token := range tokens {
			keys = append(keys, "lock:"+token)
		}
		if len(keys) > 0 {
			env.rdb.Del(context.Background(), keys...)
		}
	})

	remaining := redisInt(t, env, "group:"+groupID+":remaining")
	active := activeLockCount(t, env, groupID)

	t.Logf("C concurrency: users=%d slots=%d success=%d failed=%d sold_out=%d other_errors=%d",
		users, slots, success, users-success, soldOut, len(other))
	t.Logf("C after race: redis_remaining=%d mysql_active_locks=%d", remaining, active)

	if len(other) > 0 {
		t.Fatalf("出现 %d 个非预期错误，例如: %v", len(other), other[0])
	}
	if success > slots {
		t.Fatalf("成功锁定数 = %d，超过名额 %d（超卖）", success, slots)
	}
	if success != slots {
		t.Errorf("成功锁定数 = %d，期望 %d", success, slots)
	}
	if soldOut != users-success {
		t.Errorf("名额不足数 = %d，期望 %d", soldOut, users-success)
	}
	if remaining < 0 {
		t.Errorf("Redis remaining = %d，不能为负", remaining)
	}
	if remaining != 0 {
		t.Errorf("Redis remaining = %d，期望 0", remaining)
	}
	if active != int64(success) {
		t.Errorf("MySQL active lock 数 = %d，期望等于成功锁定数 %d", active, success)
	}
}

// TestD_NotifyRacesPayTimeout 验证：支付回调与支付超时任务竞争后状态唯一，Redis 最多回补一次。
func TestD_NotifyRacesPayTimeout(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	createdAt := time.Now().Add(-16 * time.Minute)
	orderID, enrollmentID, groupID := seedPendingOrder(t, env, createdAt, 10000)

	transactionID := "wx-race-" + uuid.NewString()
	service := paymentsvc.New(paymentrepo.NewGORM(env.db), testLogger())
	notify := newNotifyResult(orderID, transactionID, 10000)
	runner := job.NewPayTimeoutJob(env.db, env.rdb, 15*time.Minute, testLogger())
	redisKey := "group:" + groupID + ":remaining"

	var (
		wg        sync.WaitGroup
		jobResult job.PayTimeoutResult
		jobErr    error
		notifyErr error
	)
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		jobResult, jobErr = runner.Run(ctx)
	}()
	go func() {
		defer wg.Done()
		<-start
		notifyErr = service.HandleNotify(ctx, notify)
	}()
	close(start)
	wg.Wait()

	order := loadOrder(t, env, orderID)
	enrollment := loadEnrollment(t, env, enrollmentID)
	remaining := redisInt(t, env, redisKey)

	t.Logf("D race: notify_err=%v job_err=%v job_result=%+v", notifyErr, jobErr, jobResult)
	t.Logf("D final: order.status=%s enrollment.status=%s redis_remaining=%d order.paid_at=%s order.expired_at=%s",
		order.Status, enrollment.Status, remaining, formatTime(order.PaidAt), formatTime(order.ExpiredAt))

	if notifyErr != nil {
		t.Fatalf("支付回调失败: %v", notifyErr)
	}
	if jobErr != nil {
		t.Fatalf("支付超时任务失败: %v", jobErr)
	}

	switch order.Status {
	case "paid":
		if enrollment.Status != "paid" {
			t.Errorf("Order=paid 但 Enrollment=%s，期望 paid", enrollment.Status)
		}
		if remaining != 4 {
			t.Errorf("回调胜出时 Redis remaining = %d，期望 4（不回补）", remaining)
		}
	case "expired":
		if enrollment.Status != "cancelled" {
			t.Errorf("Order=expired 但 Enrollment=%s，期望 cancelled", enrollment.Status)
		}
		if remaining != 5 {
			t.Errorf("超时胜出时 Redis remaining = %d，期望 5（恰好回补一次）", remaining)
		}
	default:
		t.Fatalf("最终 Order 状态 = %q，只能是 paid 或 expired", order.Status)
	}

	second, err := runner.Run(ctx)
	if err != nil {
		t.Fatalf("第二次执行支付超时任务失败: %v", err)
	}
	remainingAfterSecond := redisInt(t, env, redisKey)
	t.Logf("D second job run: expired=%d locked=%v; redis_remaining=%d", second.Expired, second.Locked, remainingAfterSecond)

	if remainingAfterSecond != remaining {
		t.Errorf("重复执行超时任务导致 Redis 重复回补: %d -> %d", remaining, remainingAfterSecond)
	}
}

func derefString(value *string) string {
	if value == nil {
		return "NULL"
	}
	return *value
}
