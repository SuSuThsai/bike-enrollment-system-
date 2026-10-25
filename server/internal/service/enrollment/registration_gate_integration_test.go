//go:build integration

package enrollment_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"bike-enroll/server/internal/job"
	"bike-enroll/server/internal/model"
	paymentpkg "bike-enroll/server/internal/pkg/payment"
	paymentrepo "bike-enroll/server/internal/repository/payment"
	enrollmentsvc "bike-enroll/server/internal/service/enrollment"
	paymentsvc "bike-enroll/server/internal/service/payment"
)

// TestSubmitRejectedAfterDeadlineAndLockBackfills 覆盖：
// deadline 前锁定、deadline 后提交返回 EVENT_NOT_OPEN，lock 过期后名额回补。
func TestSubmitRejectedAfterDeadlineAndLockBackfills(t *testing.T) {
	env := setupEnv(t)
	eventID, groupID := seedGroup(t, env, 5)
	service := newService(env.db, env.rdb)
	ctx := context.Background()

	deadline := time.Now().Add(1200 * time.Millisecond)
	if err := env.db.Model(&model.Event{}).Where("id = ?", eventID).
		Update("registration_deadline", deadline).Error; err != nil {
		t.Fatalf("设置 deadline 失败: %v", err)
	}

	userID := uuid.NewString()
	lock := createLock(t, service, userID, groupID)
	remainingKey := "group:" + groupID + ":remaining"
	remaining, err := env.rdb.Get(ctx, remainingKey).Int()
	if err != nil || remaining != 4 {
		t.Fatalf("锁定后 Redis remaining = %d, err=%v，期望 4", remaining, err)
	}

	// 等到 deadline 之后（lock 仍在有效期内）再提交。
	time.Sleep(1600 * time.Millisecond)

	_, err = service.Submit(ctx, userID, submitInput(lock.Token, groupID))
	if !errors.Is(err, enrollmentsvc.ErrEventNotOpen) {
		t.Fatalf("deadline 后提交 err = %v，期望 ErrEventNotOpen", err)
	}
	var notOpen *enrollmentsvc.EventNotOpenError
	if !errors.As(err, &notOpen) || !strings.Contains(notOpen.Message, "截止") {
		t.Fatalf("EVENT_NOT_OPEN 消息 = %v，期望包含“截止”", err)
	}

	// 模拟 lock 自然过期并执行过期任务，名额必须回补。
	if err := env.db.Model(&model.EnrollmentLock{}).Where("token = ?", lock.Token).
		Update("expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatalf("设置 lock 过期时间失败: %v", err)
	}
	if _, err := job.NewLockExpiryJob(env.db, env.rdb, discardLogger()).Run(ctx); err != nil {
		t.Fatalf("lock 过期任务执行失败: %v", err)
	}
	remaining, err = env.rdb.Get(ctx, remainingKey).Int()
	if err != nil || remaining != 5 {
		t.Fatalf("过期任务后 Redis remaining = %d, err=%v，期望 5", remaining, err)
	}
}

// TestSubmitRejectedBeforeOpen 覆盖未开始时间窗：submit 也必须先过报名开关。
func TestSubmitRejectedBeforeOpen(t *testing.T) {
	env := setupEnv(t)
	eventID, groupID := seedGroup(t, env, 5)
	service := newService(env.db, env.rdb)
	ctx := context.Background()

	userID := uuid.NewString()
	lock := createLock(t, service, userID, groupID)

	openAt := time.Now().Add(time.Hour)
	if err := env.db.Model(&model.Event{}).Where("id = ?", eventID).
		Update("registration_open_at", openAt).Error; err != nil {
		t.Fatalf("设置 registration_open_at 失败: %v", err)
	}

	_, err := service.Submit(ctx, userID, submitInput(lock.Token, groupID))
	if !errors.Is(err, enrollmentsvc.ErrEventNotOpen) {
		t.Fatalf("未开始提交 err = %v，期望 ErrEventNotOpen", err)
	}
	var notOpen *enrollmentsvc.EventNotOpenError
	if !errors.As(err, &notOpen) || !strings.Contains(notOpen.Message, "开放") {
		t.Fatalf("EVENT_NOT_OPEN 消息 = %v，期望包含“开放”", err)
	}
}

// TestManualCloseKeepsPendingOrderPayable 覆盖手动关闭后 pending_payment 仍可支付。
func TestManualCloseKeepsPendingOrderPayable(t *testing.T) {
	env := setupEnv(t)
	eventID, groupID := seedGroup(t, env, 5)
	service := newService(env.db, env.rdb)
	ctx := context.Background()

	userID := uuid.NewString()
	lock := createLock(t, service, userID, groupID)
	submit, err := service.Submit(ctx, userID, submitInput(lock.Token, groupID))
	if err != nil {
		t.Fatalf("关闭前提交失败: %v", err)
	}

	reason := "临时维护，暂停报名"
	if err := env.db.Model(&model.Event{}).Where("id = ?", eventID).Updates(map[string]any{
		"registration_manually_closed": true,
		"registration_closed_reason":   reason,
	}).Error; err != nil {
		t.Fatalf("手动关闭失败: %v", err)
	}

	// 新锁定被拒，message 使用关闭原因。
	if _, err := service.Lock(ctx, uuid.NewString(), groupID); !errors.Is(err, enrollmentsvc.ErrEventNotOpen) {
		t.Fatalf("手动关闭后 Lock err = %v，期望 ErrEventNotOpen", err)
	} else {
		var notOpen *enrollmentsvc.EventNotOpenError
		if !errors.As(err, &notOpen) || notOpen.Message != reason {
			t.Fatalf("手动关闭消息 = %v，期望 %q", err, reason)
		}
	}

	payService := paymentsvc.New(paymentrepo.NewGORM(env.db), discardLogger())
	if err := payService.HandleNotify(ctx, &paymentpkg.NotifyResult{
		EventType:     "TRANSACTION.SUCCESS",
		OutTradeNo:    submit.Order.ID,
		TransactionID: "wx-gate-test-1",
		TradeState:    "SUCCESS",
		Amount:        paymentpkg.NotifyAmount{Total: submit.Order.Amount, Currency: "CNY"},
	}); err != nil {
		t.Fatalf("手动关闭后支付回调失败: %v", err)
	}

	var order model.Order
	if err := env.db.Where("id = ?", submit.Order.ID).First(&order).Error; err != nil {
		t.Fatalf("读取订单失败: %v", err)
	}
	if order.Status != "paid" {
		t.Fatalf("订单状态 = %q，期望 paid", order.Status)
	}
	var enrollment model.Enrollment
	if err := env.db.Where("id = ?", submit.Enrollment.ID).First(&enrollment).Error; err != nil {
		t.Fatalf("读取报名失败: %v", err)
	}
	if enrollment.Status != "paid" {
		t.Fatalf("报名状态 = %q，期望 paid", enrollment.Status)
	}
}

// TestDeadlineExtensionFlipsTimeStatusOnly 覆盖：
// 延长 deadline 后时间推导的 closed 自动恢复 open；手动关闭不受 deadline 影响。
func TestDeadlineExtensionFlipsTimeStatusOnly(t *testing.T) {
	env := setupEnv(t)
	eventID, groupID := seedGroup(t, env, 5)
	service := newService(env.db, env.rdb)
	ctx := context.Background()

	updateEvent := func(fields map[string]any) {
		t.Helper()
		if err := env.db.Model(&model.Event{}).Where("id = ?", eventID).
			Updates(fields).Error; err != nil {
			t.Fatalf("更新赛事失败: %v", err)
		}
	}
	assertNotOpen := func(wantSub string) {
		t.Helper()
		_, err := service.Lock(ctx, uuid.NewString(), groupID)
		if !errors.Is(err, enrollmentsvc.ErrEventNotOpen) {
			t.Fatalf("Lock err = %v，期望 ErrEventNotOpen", err)
		}
		var notOpen *enrollmentsvc.EventNotOpenError
		if !errors.As(err, &notOpen) || !strings.Contains(notOpen.Message, wantSub) {
			t.Fatalf("消息 = %v，期望包含 %q", err, wantSub)
		}
	}

	past := time.Now().Add(-time.Hour)
	updateEvent(map[string]any{"registration_deadline": past})
	assertNotOpen("截止")

	// 延长 deadline：时间推导的 closed 应自动变回 open。
	future := time.Now().Add(time.Hour)
	updateEvent(map[string]any{"registration_deadline": future})
	if _, err := service.Lock(ctx, uuid.NewString(), groupID); err != nil {
		t.Fatalf("延长 deadline 后 Lock 失败: %v", err)
	}

	// 手动关闭后，即使继续延长 deadline 仍保持 closed，消息使用关闭原因。
	reason := "手动关闭不受 deadline 影响"
	updateEvent(map[string]any{
		"registration_manually_closed": true,
		"registration_closed_reason":   reason,
		"registration_deadline":        future,
	})
	assertNotOpen(reason)

	updateEvent(map[string]any{"registration_deadline": future.Add(24 * time.Hour)})
	assertNotOpen(reason)
}
