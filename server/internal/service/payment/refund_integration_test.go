//go:build integration

package payment_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"bike-enroll/server/internal/model"
	paymentpkg "bike-enroll/server/internal/pkg/payment"
	paymentrepo "bike-enroll/server/internal/repository/payment"
	paymentsvc "bike-enroll/server/internal/service/payment"
)

func markOrderPaid(t *testing.T, env *notifyEnv, orderID, enrollmentID string) {
	t.Helper()
	now := time.Now()
	if err := env.db.Model(&model.Order{}).
		Where("id = ?", orderID).
		Updates(map[string]any{
			"status":         "paid",
			"paid_at":        now,
			"transaction_id": "wx-pay-" + orderID,
			"updated_at":     now,
		}).Error; err != nil {
		t.Fatalf("设置 Order paid 失败: %v", err)
	}
	if err := env.db.Model(&model.Enrollment{}).
		Where("id = ?", enrollmentID).
		Updates(map[string]any{"status": "paid", "paid_at": now, "updated_at": now}).Error; err != nil {
		t.Fatalf("设置 Enrollment paid 失败: %v", err)
	}
}

func setOrderStatus(t *testing.T, env *notifyEnv, orderID, status string) {
	t.Helper()
	if err := env.db.Model(&model.Order{}).
		Where("id = ?", orderID).
		Update("status", status).Error; err != nil {
		t.Fatalf("设置 Order 状态失败: %v", err)
	}
}

func newRefundService(env *notifyEnv, logger *slog.Logger) *paymentsvc.Service {
	service := newNotifyService(env, logger)
	service.SetRefundRepository(paymentrepo.NewGORMRefund(env.db, env.rdb))
	return service
}

func refundNotifyResult(orderID, refundNo, refundID, status string, amount int) *paymentpkg.RefundNotifyResult {
	return &paymentpkg.RefundNotifyResult{
		OutTradeNo:   orderID,
		OutRefundNo:  refundNo,
		RefundID:     refundID,
		RefundStatus: status,
		Amount:       amount,
	}
}

func loadOrder(t *testing.T, env *notifyEnv, orderID string) model.Order {
	t.Helper()
	var order model.Order
	if err := env.db.Where("id = ?", orderID).First(&order).Error; err != nil {
		t.Fatalf("读取 Order 失败: %v", err)
	}
	return order
}

func loadEnrollment(t *testing.T, env *notifyEnv, enrollmentID string) model.Enrollment {
	t.Helper()
	var enrollment model.Enrollment
	if err := env.db.Where("id = ?", enrollmentID).First(&enrollment).Error; err != nil {
		t.Fatalf("读取 Enrollment 失败: %v", err)
	}
	return enrollment
}

func TestIntegrationHandleRefundNotifySuccessAndIdempotent(t *testing.T) {
	env := setupNotifyEnv(t)
	orderID, enrollmentID, groupID := seedPendingOrder(t, env, 10000)
	markOrderPaid(t, env, orderID, enrollmentID)
	service := newRefundService(env, nil)
	result := refundNotifyResult(orderID, "refund-1", "wx-refund-1", "SUCCESS", 10000)

	if err := service.HandleRefundNotify(context.Background(), result); err != nil {
		t.Fatalf("第一次退款回调失败: %v", err)
	}
	order := loadOrder(t, env, orderID)
	enrollment := loadEnrollment(t, env, enrollmentID)
	if order.Status != "refunded" {
		t.Errorf("Order.status = %q，期望 refunded", order.Status)
	}
	if enrollment.Status != "refunded" {
		t.Errorf("Enrollment.status = %q，期望 refunded", enrollment.Status)
	}
	remaining, err := env.rdb.Get(context.Background(), "group:"+groupID+":remaining").Int()
	if err != nil {
		t.Fatalf("读取 Redis 失败: %v", err)
	}
	if remaining != 5 {
		t.Errorf("退款后 Redis remaining = %d，期望 5", remaining)
	}

	// 重复回调必须幂等：不再次回补。
	if err := service.HandleRefundNotify(context.Background(), result); err != nil {
		t.Fatalf("重复退款回调失败: %v", err)
	}
	remaining, _ = env.rdb.Get(context.Background(), "group:"+groupID+":remaining").Int()
	if remaining != 5 {
		t.Errorf("重复回调后 Redis remaining = %d，期望仍为 5", remaining)
	}
}

func TestIntegrationHandleRefundNotifyRefundingOrder(t *testing.T) {
	env := setupNotifyEnv(t)
	orderID, enrollmentID, groupID := seedPendingOrder(t, env, 10000)
	markOrderPaid(t, env, orderID, enrollmentID)
	setOrderStatus(t, env, orderID, "refunding")
	service := newRefundService(env, nil)

	if err := service.HandleRefundNotify(context.Background(), refundNotifyResult(orderID, "refund-2", "wx-refund-2", "SUCCESS", 10000)); err != nil {
		t.Fatalf("refunding 退款回调失败: %v", err)
	}
	if order := loadOrder(t, env, orderID); order.Status != "refunded" {
		t.Errorf("Order.status = %q，期望 refunded", order.Status)
	}
	if enrollment := loadEnrollment(t, env, enrollmentID); enrollment.Status != "refunded" {
		t.Errorf("Enrollment.status = %q，期望 refunded", enrollment.Status)
	}
	if remaining, _ := env.rdb.Get(context.Background(), "group:"+groupID+":remaining").Int(); remaining != 5 {
		t.Errorf("Redis remaining = %d，期望 5", remaining)
	}
}

func TestIntegrationHandleRefundNotifyNotSuccessDoesNotMigrate(t *testing.T) {
	for _, status := range []string{"CLOSED", "ABNORMAL"} {
		status := status
		t.Run(status, func(t *testing.T) {
			env := setupNotifyEnv(t)
			orderID, enrollmentID, groupID := seedPendingOrder(t, env, 10000)
			markOrderPaid(t, env, orderID, enrollmentID)

			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			service := newRefundService(env, logger)

			if err := service.HandleRefundNotify(context.Background(), refundNotifyResult(orderID, "refund-"+status, "wx-refund-"+status, status, 10000)); err != nil {
				t.Fatalf("退款回调 %s 应返回成功: %v", status, err)
			}
			if order := loadOrder(t, env, orderID); order.Status != "paid" {
				t.Errorf("Order.status = %q，期望保持 paid", order.Status)
			}
			if enrollment := loadEnrollment(t, env, enrollmentID); enrollment.Status != "paid" {
				t.Errorf("Enrollment.status = %q，期望保持 paid", enrollment.Status)
			}
			if remaining, _ := env.rdb.Get(context.Background(), "group:"+groupID+":remaining").Int(); remaining != 4 {
				t.Errorf("Redis remaining = %d，期望保持 4", remaining)
			}
			if !strings.Contains(logs.String(), "refund notify not success") {
				t.Errorf("缺少退款非成功告警日志: %s", logs.String())
			}
		})
	}
}

func TestIntegrationHandleRefundNotifyIgnoresNonRefundableOrder(t *testing.T) {
	env := setupNotifyEnv(t)
	orderID, enrollmentID, groupID := seedPendingOrder(t, env, 10000)
	service := newRefundService(env, nil)

	if err := service.HandleRefundNotify(context.Background(), refundNotifyResult(orderID, "refund-3", "wx-refund-3", "SUCCESS", 10000)); err != nil {
		t.Fatalf("pending_payment 订单退款回调应返回成功: %v", err)
	}
	if order := loadOrder(t, env, orderID); order.Status != "pending_payment" {
		t.Errorf("Order.status = %q，期望保持 pending_payment", order.Status)
	}
	if enrollment := loadEnrollment(t, env, enrollmentID); enrollment.Status != "pending_payment" {
		t.Errorf("Enrollment.status = %q，期望保持 pending_payment", enrollment.Status)
	}
	if remaining, _ := env.rdb.Get(context.Background(), "group:"+groupID+":remaining").Int(); remaining != 4 {
		t.Errorf("Redis remaining = %d，期望保持 4", remaining)
	}
}

func TestIntegrationHandleRefundNotifyUnknownOrder(t *testing.T) {
	env := setupNotifyEnv(t)
	service := newRefundService(env, nil)

	if err := service.HandleRefundNotify(context.Background(), refundNotifyResult("missing-order", "refund-4", "wx-refund-4", "SUCCESS", 10000)); err != nil {
		t.Fatalf("未知订单退款回调应返回成功: %v", err)
	}
}
