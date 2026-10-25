//go:build integration

// Package enrollment_test 覆盖报名锁定的并发集成测试。
//
// 这些用例直接连接本地 docker compose 的 MySQL/Redis 并写入真实数据，
// 因此与 internal/job 等集成测试一致，只在显式开启 integration tag 时运行，
// 避免 `go test ./...` 误用开发库（并污染压测环境）：
//
//	go test -tags=integration ./internal/service/enrollment -v -count=1
package enrollment_test

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
	"bike-enroll/server/internal/model"
	"bike-enroll/server/internal/repository"
	enrollmentrepo "bike-enroll/server/internal/repository/enrollment"
	lockrepo "bike-enroll/server/internal/repository/lock"
	enrollmentsvc "bike-enroll/server/internal/service/enrollment"
)

type testEnv struct {
	db  *gorm.DB
	rdb *redis.Client
}

func setupEnv(t *testing.T) *testEnv {
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

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func seedGroup(t *testing.T, env *testEnv, totalSlots int) (eventID, groupID string) {
	t.Helper()

	ctx := context.Background()
	now := time.Now()
	eventID = uuid.NewString()
	groupID = uuid.NewString()

	event := &model.Event{
		ID:              eventID,
		Title:           "Integration Event",
		CoverURL:        "",
		StartTime:       now.Add(24 * time.Hour),
		EndTime:         now.Add(26 * time.Hour),
		Location:        "Test Location",
		LifecycleStatus: model.LifecyclePublished,
		Description:     "Integration test event",
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
		Name:              "Integration Group",
		Price:             1000,
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

	remainingKey := "group:" + groupID + ":remaining"
	if err := env.rdb.Set(ctx, remainingKey, totalSlots, 0).Err(); err != nil {
		t.Fatalf("初始化 Redis 名额失败: %v", err)
	}

	t.Cleanup(func() {
		// 测试 teardown 清理 fixture；生产代码没有 DisclaimerSignature 的 UPDATE/DELETE 路径。
		env.db.Where("group_id = ?", groupID).Delete(&model.DisclaimerSignature{})
		env.db.Where("group_id = ?", groupID).Delete(&model.Order{})
		env.db.Where("group_id = ?", groupID).Delete(&model.Enrollment{})
		env.db.Where("group_id = ?", groupID).Delete(&model.EnrollmentLock{})
		env.db.Where("event_id = ?", eventID).Delete(&model.Group{})
		env.db.Where("id = ?", eventID).Delete(&model.Event{})
		env.rdb.Del(context.Background(), remainingKey)
	})

	return eventID, groupID
}

func newService(db *gorm.DB, rdb *redis.Client) *enrollmentsvc.Service {
	repo := lockrepo.NewGORM(db)
	submitRepo := enrollmentrepo.NewGORM(db)
	store := lockrepo.NewRedisStore(rdb)
	return enrollmentsvc.New(repo, store, submitRepo, 15*time.Minute, discardLogger())
}

func TestLockConcurrency(t *testing.T) {
	const (
		users = 100
		slots = 50
	)

	env := setupEnv(t)
	_, groupID := seedGroup(t, env, slots)
	service := newService(env.db, env.rdb)

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

	ctx := context.Background()
	errs := make(chan error, users)
	var wg sync.WaitGroup
	for _, userID := range userIDs {
		userID := userID
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := service.Lock(ctx, userID, groupID)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	success := 0
	soldOut := 0
	var other []error
	for err := range errs {
		switch {
		case err == nil:
			success++
		case errors.Is(err, enrollmentsvc.ErrGroupSoldOut):
			soldOut++
		default:
			other = append(other, err)
		}
	}

	if success > slots {
		t.Fatalf("成功锁定数 = %d，超过名额 %d", success, slots)
	}
	if success != slots {
		t.Errorf("成功锁定数 = %d，期望 %d", success, slots)
	}
	if soldOut != users-slots {
		t.Errorf("名额不足数 = %d，期望 %d", soldOut, users-slots)
	}
	if len(other) > 0 {
		t.Fatalf("出现非预期错误 %d 个，例如: %v", len(other), other[0])
	}

	remaining, err := env.rdb.Get(ctx, "group:"+groupID+":remaining").Int()
	if err != nil {
		t.Fatalf("读取 Redis 剩余名额失败: %v", err)
	}
	if remaining != 0 {
		t.Errorf("Redis 剩余名额 = %d，期望 0", remaining)
	}

	var activeLocks int64
	if err := env.db.Model(&model.EnrollmentLock{}).
		Where("group_id = ? AND status = ?", groupID, "active").
		Count(&activeLocks).Error; err != nil {
		t.Fatalf("统计 active lock 失败: %v", err)
	}
	if activeLocks != int64(success) {
		t.Errorf("active lock 数 = %d，期望 %d", activeLocks, success)
	}
}

func TestLockConcurrencyWithPaidSlots(t *testing.T) {
	const (
		totalSlots = 5
		paidSlots  = 1
		users      = 20
	)

	env := setupEnv(t)
	eventID, groupID := seedGroup(t, env, totalSlots)
	now := time.Now()
	paidEnrollment := &model.Enrollment{
		ID:        uuid.NewString(),
		UserID:    uuid.NewString(),
		EventID:   eventID,
		GroupID:   groupID,
		OrderID:   uuid.NewString(),
		Status:    "paid",
		FormData:  map[string]any{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := env.db.Create(paidEnrollment).Error; err != nil {
		t.Fatalf("创建已支付报名失败: %v", err)
	}
	remainingKey := "group:" + groupID + ":remaining"
	if err := env.rdb.Set(context.Background(), remainingKey, totalSlots-paidSlots, 0).Err(); err != nil {
		t.Fatalf("初始化 Redis 剩余名额失败: %v", err)
	}

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

	service := newService(env.db, env.rdb)
	ctx := context.Background()
	errs := make(chan error, users)
	var wg sync.WaitGroup
	for _, userID := range userIDs {
		userID := userID
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := service.Lock(ctx, userID, groupID)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	success := 0
	soldOut := 0
	for err := range errs {
		switch {
		case err == nil:
			success++
		case errors.Is(err, enrollmentsvc.ErrGroupSoldOut):
			soldOut++
		default:
			t.Fatalf("出现非预期错误: %v", err)
		}
	}

	expectedSuccess := totalSlots - paidSlots
	if success != expectedSuccess || soldOut != users-expectedSuccess {
		t.Errorf("success=%d soldOut=%d，期望 success=%d soldOut=%d",
			success, soldOut, expectedSuccess, users-expectedSuccess)
	}

	var paidCount int64
	if err := env.db.Model(&model.Enrollment{}).
		Where("group_id = ? AND status IN ?", groupID, []string{"paid", "confirmed"}).
		Count(&paidCount).Error; err != nil {
		t.Fatalf("统计已支付报名失败: %v", err)
	}
	var activeLocks int64
	if err := env.db.Model(&model.EnrollmentLock{}).
		Where("group_id = ? AND status = ?", groupID, "active").
		Count(&activeLocks).Error; err != nil {
		t.Fatalf("统计活跃锁定失败: %v", err)
	}
	if paidCount+activeLocks > totalSlots {
		t.Errorf("已支付 + 活跃锁定 = %d，超过 total_slots=%d", paidCount+activeLocks, totalSlots)
	}
	if paidCount != paidSlots || activeLocks != int64(expectedSuccess) {
		t.Errorf("paid=%d active=%d，期望 paid=%d active=%d",
			paidCount, activeLocks, paidSlots, expectedSuccess)
	}
	remaining, err := env.rdb.Get(ctx, remainingKey).Int()
	if err != nil {
		t.Fatalf("读取 Redis 剩余名额失败: %v", err)
	}
	if remaining != 0 {
		t.Errorf("Redis remaining = %d，期望 0", remaining)
	}
	t.Logf("total_slots=%d paid=%d users=%d success=%d sold_out=%d active=%d redis_remaining=%d invariant=%d<=%d",
		totalSlots, paidCount, users, success, soldOut, activeLocks, remaining,
		paidCount+activeLocks, totalSlots)
}

func TestLockSameUserRateLimited(t *testing.T) {
	env := setupEnv(t)
	_, groupID := seedGroup(t, env, 5)
	service := newService(env.db, env.rdb)

	userID := uuid.NewString()
	t.Cleanup(func() {
		env.rdb.Del(context.Background(), "lock:"+userID+":"+groupID)
	})

	ctx := context.Background()
	first, err := service.Lock(ctx, userID, groupID)
	if err != nil {
		t.Fatalf("第一次锁定失败: %v", err)
	}
	if first.RemainingSlots != 4 {
		t.Errorf("第一次锁定后 remaining_slots = %d，期望 4", first.RemainingSlots)
	}
	parsedToken, err := uuid.Parse(first.Token)
	if err != nil || parsedToken.Version() != 4 {
		t.Errorf("lock_token = %q，期望 UUID v4", first.Token)
	}
	if ttl := time.Until(first.ExpiresAt); ttl < 14*time.Minute || ttl > 16*time.Minute {
		t.Errorf("expires_at 距现在 = %v，期望约 15 分钟", ttl)
	}

	var lock model.EnrollmentLock
	if err := env.db.Where("token = ?", first.Token).First(&lock).Error; err != nil {
		t.Fatalf("读取 enrollment_locks 失败: %v", err)
	}
	if lock.Status != "active" {
		t.Errorf("lock.status = %q，期望 active", lock.Status)
	}

	_, err = service.Lock(ctx, userID, groupID)
	if !errors.Is(err, enrollmentsvc.ErrRateLimited) {
		t.Fatalf("第二次锁定 err = %v，期望 ErrRateLimited", err)
	}

	remaining, err := env.rdb.Get(ctx, "group:"+groupID+":remaining").Int()
	if err != nil {
		t.Fatalf("读取 Redis 剩余名额失败: %v", err)
	}
	if remaining != 4 {
		t.Errorf("Redis 剩余名额 = %d，期望 4", remaining)
	}
}

type failingCreateRepository struct {
	lockrepo.Repository
	token string
}

// Create 强制失败，用真实仓储做查询、真实 Redis 做回补，验证 MySQL 落库失败分支。
func (f *failingCreateRepository) Create(_ context.Context, lock *model.EnrollmentLock) error {
	f.token = lock.Token
	return errors.New("forced insert failure")
}

func TestLockRollsBackRedisOnMySQLFailure(t *testing.T) {
	env := setupEnv(t)
	_, groupID := seedGroup(t, env, 5)

	repo := &failingCreateRepository{Repository: lockrepo.NewGORM(env.db)}
	store := lockrepo.NewRedisStore(env.rdb)
	service := enrollmentsvc.New(repo, store, enrollmentrepo.NewGORM(env.db), 15*time.Minute, discardLogger())

	userID := uuid.NewString()
	t.Cleanup(func() {
		env.rdb.Del(context.Background(), "lock:"+userID+":"+groupID)
	})

	ctx := context.Background()
	if _, err := service.Lock(ctx, userID, groupID); err == nil {
		t.Fatal("MySQL 插入失败时 Lock 应返回错误")
	}
	if repo.token == "" {
		t.Fatal("未捕获失败的 lock_token")
	}

	remaining, err := env.rdb.Get(ctx, "group:"+groupID+":remaining").Int()
	if err != nil {
		t.Fatalf("读取 Redis 剩余名额失败: %v", err)
	}
	if remaining != 5 {
		t.Errorf("MySQL 失败后 Redis 剩余名额 = %d，期望回补到 5", remaining)
	}

	// 同一个 lock_token 重复回补必须幂等，不能把名额虚增到 6。
	if err := store.Rollback(ctx, groupID, repo.token); err != nil {
		t.Fatalf("重复回补失败: %v", err)
	}
	remaining, err = env.rdb.Get(ctx, "group:"+groupID+":remaining").Int()
	if err != nil {
		t.Fatalf("重复回补后读取 Redis 失败: %v", err)
	}
	if remaining != 5 {
		t.Errorf("重复回补后 Redis 剩余名额 = %d，期望仍为 5", remaining)
	}

	var count int64
	if err := env.db.Model(&model.EnrollmentLock{}).
		Where("group_id = ?", groupID).
		Count(&count).Error; err != nil {
		t.Fatalf("统计锁记录失败: %v", err)
	}
	if count != 0 {
		t.Errorf("MySQL 失败后锁记录数 = %d，期望 0", count)
	}
}

func TestSubmitCreatesPendingEnrollmentAndOrder(t *testing.T) {
	env := setupEnv(t)
	_, groupID := seedGroup(t, env, 5)
	service := newService(env.db, env.rdb)

	userID := uuid.NewString()
	lock := createLock(t, service, userID, groupID)
	t.Cleanup(func() {
		env.rdb.Del(context.Background(), "lock:"+userID+":"+groupID, "lock:"+lock.Token)
	})

	ctx := context.Background()
	before, err := env.rdb.Get(ctx, "group:"+groupID+":remaining").Int()
	if err != nil {
		t.Fatalf("读取 Redis 失败: %v", err)
	}

	result, err := service.Submit(ctx, userID, submitInput(lock.Token, groupID))
	if err != nil {
		t.Fatalf("提交报名失败: %v", err)
	}
	if result.Enrollment.Status != "pending_payment" {
		t.Errorf("enrollment.status = %q，期望 pending_payment", result.Enrollment.Status)
	}
	if result.Order.Status != "pending_payment" {
		t.Errorf("order.status = %q，期望 pending_payment", result.Order.Status)
	}
	if result.Order.Amount != 1000 {
		t.Errorf("order.amount = %d，期望 1000", result.Order.Amount)
	}
	if len(result.Order.ID) > 32 {
		t.Errorf("order.id 长度 = %d，微信 out_trade_no 上限 32", len(result.Order.ID))
	}
	if result.Enrollment.OrderID != result.Order.ID || result.Order.EnrollmentID != result.Enrollment.ID {
		t.Errorf("Enrollment/Order 关联不一致: %+v / %+v", result.Enrollment, result.Order)
	}

	var lockRow model.EnrollmentLock
	if err := env.db.Where("token = ?", lock.Token).First(&lockRow).Error; err != nil {
		t.Fatalf("读取 lock 失败: %v", err)
	}
	if lockRow.Status != "consumed" {
		t.Errorf("lock.status = %q，期望 consumed", lockRow.Status)
	}

	var enrollmentCount, orderCount int64
	env.db.Model(&model.Enrollment{}).Where("group_id = ?", groupID).Count(&enrollmentCount)
	env.db.Model(&model.Order{}).Where("group_id = ?", groupID).Count(&orderCount)
	if enrollmentCount != 1 || orderCount != 1 {
		t.Errorf("enrollment/order 数 = %d/%d，期望 1/1", enrollmentCount, orderCount)
	}

	var signature model.DisclaimerSignature
	if err := env.db.Where("enrollment_id = ?", result.Enrollment.ID).First(&signature).Error; err != nil {
		t.Fatalf("读取免责签署记录失败: %v", err)
	}
	if signature.DisclaimerVersion != "v1" || signature.ClientIP == "" || signature.UserAgent == "" {
		t.Errorf("免责签署记录不完整: %+v", signature)
	}

	after, err := env.rdb.Get(ctx, "group:"+groupID+":remaining").Int()
	if err != nil {
		t.Fatalf("读取 Redis 失败: %v", err)
	}
	if after != before {
		t.Errorf("提交接口不应修改 Redis：提交前 %d，提交后 %d", before, after)
	}
}

func TestSubmitExpiredLockReturnsLockExpired(t *testing.T) {
	env := setupEnv(t)
	_, groupID := seedGroup(t, env, 5)
	service := newService(env.db, env.rdb)

	userID := uuid.NewString()
	lock := createLock(t, service, userID, groupID)
	t.Cleanup(func() {
		env.rdb.Del(context.Background(), "lock:"+userID+":"+groupID, "lock:"+lock.Token)
	})

	ctx := context.Background()
	if err := env.db.Exec(
		"UPDATE enrollment_locks SET expires_at = NOW(3) - INTERVAL 1 MINUTE WHERE token = ?",
		lock.Token,
	).Error; err != nil {
		t.Fatalf("设置 lock 过期失败: %v", err)
	}

	_, err := service.Submit(ctx, userID, submitInput(lock.Token, groupID))
	if !errors.Is(err, enrollmentsvc.ErrLockExpired) {
		t.Fatalf("Submit err = %v，期望 ErrLockExpired", err)
	}

	var count int64
	env.db.Model(&model.Enrollment{}).Where("group_id = ?", groupID).Count(&count)
	if count != 0 {
		t.Errorf("过期 lock 不应创建 Enrollment，实际 %d", count)
	}
}

func TestSubmitConsumedLockReturnsAlreadyConsumed(t *testing.T) {
	env := setupEnv(t)
	_, groupID := seedGroup(t, env, 5)
	service := newService(env.db, env.rdb)

	userID := uuid.NewString()
	lock := createLock(t, service, userID, groupID)
	t.Cleanup(func() {
		env.rdb.Del(context.Background(), "lock:"+userID+":"+groupID, "lock:"+lock.Token)
	})

	if err := env.db.Model(&model.EnrollmentLock{}).
		Where("token = ?", lock.Token).
		Updates(map[string]any{"status": "consumed", "consumed_at": time.Now()}).Error; err != nil {
		t.Fatalf("设置 lock consumed 失败: %v", err)
	}

	_, err := service.Submit(context.Background(), userID, submitInput(lock.Token, groupID))
	if !errors.Is(err, enrollmentsvc.ErrLockAlreadyConsumed) {
		t.Fatalf("Submit err = %v，期望 ErrLockAlreadyConsumed", err)
	}

	var count int64
	env.db.Model(&model.Enrollment{}).Where("group_id = ?", groupID).Count(&count)
	if count != 0 {
		t.Errorf("已消费 lock 不应创建 Enrollment，实际 %d", count)
	}
}

func TestSubmitDisclaimerVersionMismatch(t *testing.T) {
	env := setupEnv(t)
	_, groupID := seedGroup(t, env, 5)
	service := newService(env.db, env.rdb)

	userID := uuid.NewString()
	lock := createLock(t, service, userID, groupID)
	t.Cleanup(func() {
		env.rdb.Del(context.Background(), "lock:"+userID+":"+groupID, "lock:"+lock.Token)
	})

	input := submitInput(lock.Token, groupID)
	input.DisclaimerVersion = "v2"
	_, err := service.Submit(context.Background(), userID, input)
	if !errors.Is(err, enrollmentsvc.ErrDisclaimerVersionMismatch) {
		t.Fatalf("Submit err = %v，期望 ErrDisclaimerVersionMismatch", err)
	}

	var count int64
	env.db.Model(&model.Enrollment{}).Where("group_id = ?", groupID).Count(&count)
	if count != 0 {
		t.Errorf("免责版本不一致不应创建 Enrollment，实际 %d", count)
	}
}

func TestSubmitDuplicateEnrollment(t *testing.T) {
	env := setupEnv(t)
	_, groupID := seedGroup(t, env, 5)
	service := newService(env.db, env.rdb)

	userID := uuid.NewString()
	first := createLock(t, service, userID, groupID)
	if _, err := service.Submit(context.Background(), userID, submitInput(first.Token, groupID)); err != nil {
		t.Fatalf("第一次提交失败: %v", err)
	}

	secondToken := uuid.NewString()
	now := time.Now()
	second := &model.EnrollmentLock{
		Token:     secondToken,
		UserID:    userID,
		GroupID:   groupID,
		Status:    "active",
		ExpiresAt: now.Add(15 * time.Minute),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := env.db.Create(second).Error; err != nil {
		t.Fatalf("创建第二个 lock 失败: %v", err)
	}
	t.Cleanup(func() {
		env.rdb.Del(context.Background(), "lock:"+userID+":"+groupID, "lock:"+first.Token, "lock:"+secondToken)
	})

	_, err := service.Submit(context.Background(), userID, submitInput(secondToken, groupID))
	if !errors.Is(err, enrollmentsvc.ErrEnrollmentDuplicate) {
		t.Fatalf("重复报名 err = %v，期望 ErrEnrollmentDuplicate", err)
	}

	var secondRow model.EnrollmentLock
	if err := env.db.Where("token = ?", secondToken).First(&secondRow).Error; err != nil {
		t.Fatalf("读取第二个 lock 失败: %v", err)
	}
	if secondRow.Status != "active" {
		t.Errorf("重复报名事务回滚后 lock.status = %q，期望 active", secondRow.Status)
	}

	var count int64
	env.db.Model(&model.Enrollment{}).Where("group_id = ?", groupID).Count(&count)
	if count != 1 {
		t.Errorf("重复报名后 Enrollment 数 = %d，期望 1", count)
	}
}

func createLock(t *testing.T, service *enrollmentsvc.Service, userID, groupID string) *enrollmentsvc.LockResult {
	t.Helper()
	lock, err := service.Lock(context.Background(), userID, groupID)
	if err != nil {
		t.Fatalf("创建 lock 失败: %v", err)
	}
	return lock
}

func submitInput(lockToken, groupID string) enrollmentsvc.SubmitInput {
	return enrollmentsvc.SubmitInput{
		LockToken:          lockToken,
		GroupID:            groupID,
		FormData:           map[string]any{"name": "Integration Tester"},
		DisclaimerVersion:  "v1",
		DisclaimerSignedAt: time.Now(),
		ClientIP:           "127.0.0.1",
		UserAgent:          "integration-test",
	}
}
