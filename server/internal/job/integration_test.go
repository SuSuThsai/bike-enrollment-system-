//go:build integration

package job_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
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

type jobEnv struct {
	db  *gorm.DB
	rdb *redis.Client
}

func setupJobEnv(t *testing.T) *jobEnv {
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
	return &jobEnv{db: db, rdb: rdb}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func seedExpiredLock(t *testing.T, env *jobEnv) (lockID, groupID string) {
	t.Helper()
	now := time.Now()
	lockID = uuid.NewString()
	groupID = uuid.NewString()
	lock := &model.EnrollmentLock{
		Token:     lockID,
		UserID:    uuid.NewString(),
		GroupID:   groupID,
		Status:    "active",
		ExpiresAt: now.Add(-time.Minute),
		CreatedAt: now.Add(-2 * time.Minute),
		UpdatedAt: now.Add(-2 * time.Minute),
	}
	if err := env.db.Create(lock).Error; err != nil {
		t.Fatalf("创建过期 lock 失败: %v", err)
	}
	key := "group:" + groupID + ":remaining"
	if err := env.rdb.Set(context.Background(), key, 4, 0).Err(); err != nil {
		t.Fatalf("初始化 Redis 失败: %v", err)
	}
	t.Cleanup(func() {
		env.db.Where("token = ?", lockID).Delete(&model.EnrollmentLock{})
		env.rdb.Del(context.Background(), key)
	})
	return lockID, groupID
}

func seedTimeoutOrder(t *testing.T, env *jobEnv) (orderID, enrollmentID, groupID string) {
	t.Helper()
	now := time.Now()
	createdAt := now.Add(-16 * time.Minute)
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
		FormData:  map[string]any{},
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
	order := &model.Order{
		ID:           orderID,
		EnrollmentID: enrollmentID,
		UserID:       userID,
		GroupID:      groupID,
		Amount:       10000,
		Status:       "pending_payment",
		CreatedAt:    createdAt,
		UpdatedAt:    createdAt,
	}
	if err := env.db.Create(enrollment).Error; err != nil {
		t.Fatalf("创建 Enrollment 失败: %v", err)
	}
	if err := env.db.Create(order).Error; err != nil {
		t.Fatalf("创建 Order 失败: %v", err)
	}
	key := "group:" + groupID + ":remaining"
	if err := env.rdb.Set(context.Background(), key, 4, 0).Err(); err != nil {
		t.Fatalf("初始化 Redis 失败: %v", err)
	}
	t.Cleanup(func() {
		env.db.Where("id = ?", orderID).Delete(&model.Order{})
		env.db.Where("id = ?", enrollmentID).Delete(&model.Enrollment{})
		env.rdb.Del(context.Background(), key)
	})
	return orderID, enrollmentID, groupID
}

func TestLockExpiryJobReclaimsOnce(t *testing.T) {
	env := setupJobEnv(t)
	lockID, groupID := seedExpiredLock(t, env)
	jobRunner := job.NewLockExpiryJob(env.db, env.rdb, testLogger())

	result, err := jobRunner.Run(context.Background())
	if err != nil {
		t.Fatalf("第一次执行失败: %v", err)
	}
	if result.Locked || result.Reclaimed != 1 {
		t.Errorf("result = %+v，期望 Reclaimed=1、Locked=false", result)
	}

	var lock model.EnrollmentLock
	if err := env.db.Where("token = ?", lockID).First(&lock).Error; err != nil {
		t.Fatalf("读取 lock 失败: %v", err)
	}
	if lock.Status != "expired" || lock.ExpiredAt == nil {
		t.Errorf("lock 未正确过期: %+v", lock)
	}
	remaining, err := env.rdb.Get(context.Background(), "group:"+groupID+":remaining").Int()
	if err != nil {
		t.Fatalf("读取 Redis 失败: %v", err)
	}
	if remaining != 5 {
		t.Errorf("Redis remaining = %d，期望 5", remaining)
	}

	second, err := jobRunner.Run(context.Background())
	if err != nil {
		t.Fatalf("第二次执行失败: %v", err)
	}
	if second.Reclaimed != 0 {
		t.Errorf("重复执行 Reclaimed = %d，期望 0", second.Reclaimed)
	}
	remaining, _ = env.rdb.Get(context.Background(), "group:"+groupID+":remaining").Int()
	if remaining != 5 {
		t.Errorf("重复执行后 Redis remaining = %d，期望仍为 5", remaining)
	}
}

func TestLockExpiryJobHonorsJobLock(t *testing.T) {
	env := setupJobEnv(t)
	lockID, groupID := seedExpiredLock(t, env)
	ctx := context.Background()
	if err := env.rdb.Set(ctx, "job:lock-expiry:lock", "other-instance", time.Minute).Err(); err != nil {
		t.Fatalf("设置任务锁失败: %v", err)
	}
	t.Cleanup(func() { env.rdb.Del(context.Background(), "job:lock-expiry:lock") })

	jobRunner := job.NewLockExpiryJob(env.db, env.rdb, testLogger())
	result, err := jobRunner.Run(ctx)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if !result.Locked || result.Reclaimed != 0 {
		t.Errorf("持有任务锁时 result = %+v，期望 Locked=true、Reclaimed=0", result)
	}
	var lock model.EnrollmentLock
	if err := env.db.Where("token = ?", lockID).First(&lock).Error; err != nil {
		t.Fatalf("读取 lock 失败: %v", err)
	}
	if lock.Status != "active" {
		t.Errorf("未获得任务锁时不应修改 lock，实际 status=%s", lock.Status)
	}
	remaining, _ := env.rdb.Get(ctx, "group:"+groupID+":remaining").Int()
	if remaining != 4 {
		t.Errorf("未获得任务锁时 Redis remaining = %d，期望 4", remaining)
	}
}

func TestPayTimeoutJobExpiresOnce(t *testing.T) {
	env := setupJobEnv(t)
	orderID, enrollmentID, groupID := seedTimeoutOrder(t, env)
	jobRunner := job.NewPayTimeoutJob(env.db, env.rdb, 15*time.Minute, testLogger())

	result, err := jobRunner.Run(context.Background())
	if err != nil {
		t.Fatalf("第一次执行失败: %v", err)
	}
	if result.Locked || result.Expired != 1 {
		t.Errorf("result = %+v，期望 Expired=1、Locked=false", result)
	}

	var order model.Order
	var enrollment model.Enrollment
	env.db.Where("id = ?", orderID).First(&order)
	env.db.Where("id = ?", enrollmentID).First(&enrollment)
	if order.Status != "expired" || order.ExpiredAt == nil {
		t.Errorf("order 未正确过期: %+v", order)
	}
	if enrollment.Status != "cancelled" || enrollment.CancelledAt == nil {
		t.Errorf("enrollment 未正确取消: %+v", enrollment)
	}
	remaining, _ := env.rdb.Get(context.Background(), "group:"+groupID+":remaining").Int()
	if remaining != 5 {
		t.Errorf("Redis remaining = %d，期望 5", remaining)
	}

	second, err := jobRunner.Run(context.Background())
	if err != nil {
		t.Fatalf("第二次执行失败: %v", err)
	}
	if second.Expired != 0 {
		t.Errorf("重复执行 Expired = %d，期望 0", second.Expired)
	}
	remaining, _ = env.rdb.Get(context.Background(), "group:"+groupID+":remaining").Int()
	if remaining != 5 {
		t.Errorf("重复执行后 Redis remaining = %d，期望仍为 5", remaining)
	}
}

func TestReconcileJobDetectsMismatch(t *testing.T) {
	env := setupJobEnv(t)
	now := time.Now()
	groupID := uuid.NewString()
	group := &model.Group{
		ID: groupID, EventID: uuid.NewString(), Name: "对账组",
		Price: 1000, TotalSlots: 10, StartTime: now.Add(24 * time.Hour),
		Status: "open", FormSchema: map[string]any{}, DisclaimerVersion: "v1",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := env.db.Create(group).Error; err != nil {
		t.Fatalf("创建 group 失败: %v", err)
	}
	for i := 0; i < 2; i++ {
		lock := &model.EnrollmentLock{
			Token: uuid.NewString(), UserID: uuid.NewString(), GroupID: groupID,
			Status: "active", ExpiresAt: now.Add(15 * time.Minute),
			CreatedAt: now, UpdatedAt: now,
		}
		if err := env.db.Create(lock).Error; err != nil {
			t.Fatalf("创建 active lock 失败: %v", err)
		}
	}
	for _, status := range []string{"pending_payment", "paid", "confirmed"} {
		enrollment := &model.Enrollment{
			ID: uuid.NewString(), UserID: uuid.NewString(), EventID: group.EventID,
			GroupID: groupID, OrderID: uuid.NewString(), Status: status,
			FormData: map[string]any{}, CreatedAt: now, UpdatedAt: now,
		}
		if err := env.db.Create(enrollment).Error; err != nil {
			t.Fatalf("创建 enrollment 失败: %v", err)
		}
	}
	key := "group:" + groupID + ":remaining"
	if err := env.rdb.Set(context.Background(), key, 5, 0).Err(); err != nil {
		t.Fatalf("初始化 Redis 失败: %v", err)
	}
	t.Cleanup(func() {
		env.db.Where("group_id = ?", groupID).Delete(&model.Enrollment{})
		env.db.Where("group_id = ?", groupID).Delete(&model.EnrollmentLock{})
		env.db.Where("id = ?", groupID).Delete(&model.Group{})
		env.rdb.Del(context.Background(), key)
	})

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	reconciler := job.NewReconcileJob(env.db, env.rdb, logger)

	// 对账任务会扫描全库；测试只断言本用例组别的结果，避免受其他数据影响。
	findGroup := func(result job.ReconcileResult, groupID string) *job.GroupReconcileResult {
		for i := range result.Groups {
			if result.Groups[i].GroupID == groupID {
				return &result.Groups[i]
			}
		}
		return nil
	}

	result, err := reconciler.Run(context.Background())
	if err != nil {
		t.Fatalf("第一次对账失败: %v", err)
	}
	if result.Mismatches != 0 {
		t.Fatalf("第一次对账存在不一致: %+v", result)
	}
	groupResult := findGroup(result, groupID)
	if groupResult == nil {
		t.Fatalf("第一次对账未包含本用例组别 %s", groupID)
	}
	if groupResult.RedisRemaining != 5 || groupResult.ActiveLocks != 2 ||
		groupResult.PendingPayment != 1 || groupResult.PaidConfirmed != 2 ||
		groupResult.TotalSlots != 10 || !groupResult.Consistent {
		t.Errorf("对账计数错误: %+v", groupResult)
	}

	if err := env.rdb.Set(context.Background(), key, 4, 0).Err(); err != nil {
		t.Fatalf("修改 Redis 失败: %v", err)
	}
	result, err = reconciler.Run(context.Background())
	if err != nil {
		t.Fatalf("第二次对账失败: %v", err)
	}
	groupResult = findGroup(result, groupID)
	if result.Mismatches != 1 || groupResult == nil || groupResult.Difference != -1 {
		t.Errorf("不一致对账结果异常: %+v", result)
	}
	logText := logs.String()
	for _, expected := range []string{
		"reconciliation mismatch",
		"alert=true",
		"RECONCILIATION_MISMATCH",
		"redis_remaining=4",
		"active_locks=2",
		"pending_payment=1",
		"paid_confirmed=2",
		"total_slots=10",
	} {
		if !strings.Contains(logText, expected) {
			t.Errorf("对账日志缺少 %q: %s", expected, logText)
		}
	}
}

func TestPayTimeoutJobConcurrentWithNotify(t *testing.T) {
	env := setupJobEnv(t)
	orderID, enrollmentID, groupID := seedTimeoutOrder(t, env)
	jobRunner := job.NewPayTimeoutJob(env.db, env.rdb, 15*time.Minute, testLogger())

	paymentService := paymentsvc.New(paymentrepo.NewGORM(env.db), testLogger())
	notifyResult := &paymentpkg.NotifyResult{
		EventType:     "TRANSACTION.SUCCESS",
		OutTradeNo:    orderID,
		TransactionID: "wx-transaction-1",
		TradeState:    "SUCCESS",
		Amount:        paymentpkg.NotifyAmount{Total: 10000, Currency: "CNY"},
	}

	start := make(chan struct{})
	var jobErr, notifyErr error
	done := make(chan struct{}, 2)
	go func() {
		<-start
		_, jobErr = jobRunner.Run(context.Background())
		done <- struct{}{}
	}()
	go func() {
		<-start
		notifyErr = paymentService.HandleNotify(context.Background(), notifyResult)
		done <- struct{}{}
	}()
	close(start)
	<-done
	<-done

	if jobErr != nil {
		t.Fatalf("超时任务失败: %v", jobErr)
	}
	if notifyErr != nil {
		t.Fatalf("回调失败: %v", notifyErr)
	}

	var order model.Order
	var enrollment model.Enrollment
	env.db.Where("id = ?", orderID).First(&order)
	env.db.Where("id = ?", enrollmentID).First(&enrollment)
	remaining, _ := env.rdb.Get(context.Background(), "group:"+groupID+":remaining").Int()

	switch order.Status {
	case "paid":
		if enrollment.Status != "paid" {
			t.Errorf("Order paid 但 Enrollment = %s", enrollment.Status)
		}
		if remaining != 4 {
			t.Errorf("回调胜出时 Redis remaining = %d，期望 4", remaining)
		}
	case "expired":
		if enrollment.Status != "cancelled" {
			t.Errorf("Order expired 但 Enrollment = %s", enrollment.Status)
		}
		if remaining != 5 {
			t.Errorf("超时胜出时 Redis remaining = %d，期望 5", remaining)
		}
	default:
		t.Fatalf("并发后 Order 状态异常: %s", order.Status)
	}
}
