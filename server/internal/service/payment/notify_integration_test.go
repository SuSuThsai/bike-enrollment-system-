//go:build integration

package payment_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"bike-enroll/server/internal/config"
	"bike-enroll/server/internal/model"
	paymentpkg "bike-enroll/server/internal/pkg/payment"
	"bike-enroll/server/internal/repository"
	paymentrepo "bike-enroll/server/internal/repository/payment"
	paymentsvc "bike-enroll/server/internal/service/payment"
)

type notifyEnv struct {
	db  *gorm.DB
	rdb *redis.Client
}

func setupNotifyEnv(t *testing.T) *notifyEnv {
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
	return &notifyEnv{db: db, rdb: rdb}
}

type fakeEnrollmentNotifier struct {
	orderIDs []string
}

func (f *fakeEnrollmentNotifier) EnqueueEnrollmentSuccess(orderID, _ string) {
	f.orderIDs = append(f.orderIDs, orderID)
}

func seedPendingOrder(t *testing.T, env *notifyEnv, amount int) (orderID, enrollmentID, groupID string) {
	t.Helper()
	now := time.Now()
	userID := uuid.NewString()
	eventID := uuid.NewString()
	groupID = uuid.NewString()
	enrollmentID = uuid.NewString()
	orderID = strings.ReplaceAll(uuid.NewString(), "-", "")

	enrollment := &model.Enrollment{
		ID:        enrollmentID,
		UserID:    userID,
		EventID:   eventID,
		GroupID:   groupID,
		OrderID:   orderID,
		Status:    "pending_payment",
		FormData:  map[string]any{"name": "notify test"},
		CreatedAt: now,
		UpdatedAt: now,
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
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := env.db.Create(order).Error; err != nil {
		t.Fatalf("创建 Order 失败: %v", err)
	}

	remainingKey := "group:" + groupID + ":remaining"
	if err := env.rdb.Set(context.Background(), remainingKey, 4, 0).Err(); err != nil {
		t.Fatalf("初始化 Redis 失败: %v", err)
	}
	t.Cleanup(func() {
		env.db.Where("id = ?", orderID).Delete(&model.Order{})
		env.db.Where("id = ?", enrollmentID).Delete(&model.Enrollment{})
		env.rdb.Del(context.Background(), remainingKey)
	})
	return orderID, enrollmentID, groupID
}

func notifyResult(orderID string, amount int, transactionID string) *paymentpkg.NotifyResult {
	return &paymentpkg.NotifyResult{
		EventType:     "TRANSACTION.SUCCESS",
		OutTradeNo:    orderID,
		TransactionID: transactionID,
		TradeState:    "SUCCESS",
		Amount:        paymentpkg.NotifyAmount{Total: amount, Currency: "CNY"},
	}
}

func newNotifyService(env *notifyEnv, logger *slog.Logger) *paymentsvc.Service {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	}
	return paymentsvc.New(paymentrepo.NewGORM(env.db), logger)
}

func TestIntegrationHandleNotifyAppliesPayment(t *testing.T) {
	env := setupNotifyEnv(t)
	orderID, enrollmentID, groupID := seedPendingOrder(t, env, 10000)
	service := newNotifyService(env, nil)
	notifier := &fakeEnrollmentNotifier{}
	service.SetNotifier(notifier)

	if err := service.HandleNotify(context.Background(), notifyResult(orderID, 10000, "wx-transaction-1")); err != nil {
		t.Fatalf("HandleNotify 失败: %v", err)
	}
	if len(notifier.orderIDs) != 1 || notifier.orderIDs[0] != orderID {
		t.Errorf("支付成功应异步投递一次通知: %+v", notifier.orderIDs)
	}

	var order model.Order
	if err := env.db.Where("id = ?", orderID).First(&order).Error; err != nil {
		t.Fatalf("读取 Order 失败: %v", err)
	}
	if order.Status != "paid" || order.TransactionID == nil || *order.TransactionID != "wx-transaction-1" || order.PaidAt == nil {
		t.Errorf("Order 未正确迁移: %+v", order)
	}

	var enrollment model.Enrollment
	if err := env.db.Where("id = ?", enrollmentID).First(&enrollment).Error; err != nil {
		t.Fatalf("读取 Enrollment 失败: %v", err)
	}
	if enrollment.Status != "paid" || enrollment.PaidAt == nil {
		t.Errorf("Enrollment 未正确迁移: %+v", enrollment)
	}

	remaining, err := env.rdb.Get(context.Background(), "group:"+groupID+":remaining").Int()
	if err != nil {
		t.Fatalf("读取 Redis 失败: %v", err)
	}
	if remaining != 4 {
		t.Errorf("回调不应修改 Redis：remaining = %d，期望 4", remaining)
	}
}

func TestIntegrationHandleNotifyRepeatedCallbackIsIdempotent(t *testing.T) {
	env := setupNotifyEnv(t)
	orderID, enrollmentID, groupID := seedPendingOrder(t, env, 10000)
	service := newNotifyService(env, nil)
	notifier := &fakeEnrollmentNotifier{}
	service.SetNotifier(notifier)
	result := notifyResult(orderID, 10000, "wx-transaction-1")

	if err := service.HandleNotify(context.Background(), result); err != nil {
		t.Fatalf("第一次 HandleNotify 失败: %v", err)
	}
	var firstOrder model.Order
	var firstEnrollment model.Enrollment
	env.db.Where("id = ?", orderID).First(&firstOrder)
	env.db.Where("id = ?", enrollmentID).First(&firstEnrollment)
	firstRemaining, err := env.rdb.Get(context.Background(), "group:"+groupID+":remaining").Int()
	if err != nil {
		t.Fatalf("第一次回调后读取 Redis 失败: %v", err)
	}

	if err := service.HandleNotify(context.Background(), result); err != nil {
		t.Fatalf("第二次 HandleNotify 失败: %v", err)
	}
	var secondOrder model.Order
	var secondEnrollment model.Enrollment
	env.db.Where("id = ?", orderID).First(&secondOrder)
	env.db.Where("id = ?", enrollmentID).First(&secondEnrollment)
	secondRemaining, err := env.rdb.Get(context.Background(), "group:"+groupID+":remaining").Int()
	if err != nil {
		t.Fatalf("第二次回调后读取 Redis 失败: %v", err)
	}

	if secondOrder.Status != "paid" || secondEnrollment.Status != "paid" {
		t.Errorf("重复回调后状态异常: order=%s enrollment=%s", secondOrder.Status, secondEnrollment.Status)
	}
	if firstOrder.TransactionID == nil || secondOrder.TransactionID == nil ||
		*firstOrder.TransactionID != *secondOrder.TransactionID {
		t.Error("重复回调不应改变 Order.transaction_id")
	}
	if firstOrder.PaidAt == nil || secondOrder.PaidAt == nil || !firstOrder.PaidAt.Equal(*secondOrder.PaidAt) {
		t.Error("重复回调不应更新 Order.paid_at")
	}
	if firstEnrollment.PaidAt == nil || secondEnrollment.PaidAt == nil || !firstEnrollment.PaidAt.Equal(*secondEnrollment.PaidAt) {
		t.Error("重复回调不应更新 Enrollment.paid_at")
	}
	if !firstOrder.UpdatedAt.Equal(secondOrder.UpdatedAt) || !firstEnrollment.UpdatedAt.Equal(secondEnrollment.UpdatedAt) {
		t.Error("重复回调不应更新 updated_at")
	}
	if firstRemaining != 4 || secondRemaining != 4 {
		t.Errorf("重复回调不应改变 Redis：第一次=%d 第二次=%d，期望均为 4", firstRemaining, secondRemaining)
	}
	if len(notifier.orderIDs) != 1 {
		t.Errorf("重复回调只应投递一次通知，实际 %d 次", len(notifier.orderIDs))
	}
}

func TestIntegrationHandleNotifyAmountMismatchDoesNotMigrate(t *testing.T) {
	env := setupNotifyEnv(t)
	orderID, enrollmentID, groupID := seedPendingOrder(t, env, 10000)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	service := newNotifyService(env, logger)
	notifier := &fakeEnrollmentNotifier{}
	service.SetNotifier(notifier)

	if err := service.HandleNotify(context.Background(), notifyResult(orderID, 9999, "wx-transaction-1")); err != nil {
		t.Fatalf("金额不符应返回成功，实际 %v", err)
	}

	var order model.Order
	var enrollment model.Enrollment
	env.db.Where("id = ?", orderID).First(&order)
	env.db.Where("id = ?", enrollmentID).First(&enrollment)
	if order.Status != "pending_payment" || enrollment.Status != "pending_payment" {
		t.Errorf("金额不符不应迁移: order=%s enrollment=%s", order.Status, enrollment.Status)
	}
	if order.TransactionID != nil {
		t.Errorf("金额不符不应写入 transaction_id: %v", *order.TransactionID)
	}
	if !strings.Contains(logs.String(), "PAY_AMOUNT_MISMATCH") {
		t.Errorf("缺少金额不符告警日志: %s", logs.String())
	}
	remaining, _ := env.rdb.Get(context.Background(), "group:"+groupID+":remaining").Int()
	if remaining != 4 {
		t.Errorf("金额不符不应修改 Redis：remaining = %d", remaining)
	}
	if len(notifier.orderIDs) != 0 {
		t.Errorf("金额不符不应投递通知，实际 %d 次", len(notifier.orderIDs))
	}
}

func TestIntegrationHandleNotifyIgnoresNonSuccessEvent(t *testing.T) {
	env := setupNotifyEnv(t)
	orderID, enrollmentID, _ := seedPendingOrder(t, env, 10000)
	service := newNotifyService(env, nil)

	result := notifyResult(orderID, 10000, "wx-transaction-1")
	result.EventType = "REFUND.SUCCESS"
	if err := service.HandleNotify(context.Background(), result); err != nil {
		t.Fatalf("非成功事件应返回成功: %v", err)
	}

	var order model.Order
	var enrollment model.Enrollment
	env.db.Where("id = ?", orderID).First(&order)
	env.db.Where("id = ?", enrollmentID).First(&enrollment)
	if order.Status != "pending_payment" || enrollment.Status != "pending_payment" {
		t.Errorf("非成功事件不应迁移: order=%s enrollment=%s", order.Status, enrollment.Status)
	}
}

func TestIntegrationHandleNotifyConcurrentWithPaymentTimeout(t *testing.T) {
	env := setupNotifyEnv(t)
	orderID, enrollmentID, groupID := seedPendingOrder(t, env, 10000)
	service := newNotifyService(env, nil)

	start := make(chan struct{})
	var wg sync.WaitGroup
	var notifyErr, timeoutErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		notifyErr = service.HandleNotify(context.Background(), notifyResult(orderID, 10000, "wx-transaction-1"))
	}()
	go func() {
		defer wg.Done()
		<-start
		timeoutErr = applyPaymentTimeout(env, orderID)
	}()
	close(start)
	wg.Wait()

	if notifyErr != nil {
		t.Fatalf("并发回调失败: %v", notifyErr)
	}
	if timeoutErr != nil {
		t.Fatalf("并发超时任务失败: %v", timeoutErr)
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
	if enrollment.Status == "pending_payment" {
		t.Error("并发后 Enrollment 不应仍为 pending_payment")
	}
}

func applyPaymentTimeout(env *notifyEnv, orderID string) error {
	ctx := context.Background()
	return env.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var order model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", orderID).First(&order).Error; err != nil {
			return err
		}
		if order.Status != "pending_payment" {
			return nil
		}
		now := time.Now()
		update := tx.Model(&model.Order{}).
			Where("id = ? AND status = ?", orderID, "pending_payment").
			Updates(map[string]any{"status": "expired", "updated_at": now})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return nil
		}
		enrollmentUpdate := tx.Model(&model.Enrollment{}).
			Where("order_id = ? AND status = ?", orderID, "pending_payment").
			Updates(map[string]any{"status": "cancelled", "updated_at": now})
		if enrollmentUpdate.Error != nil {
			return enrollmentUpdate.Error
		}
		if enrollmentUpdate.RowsAffected != 1 {
			return errors.New("pending enrollment not found")
		}
		return env.rdb.Incr(ctx, "group:"+order.GroupID+":remaining").Err()
	})
}
