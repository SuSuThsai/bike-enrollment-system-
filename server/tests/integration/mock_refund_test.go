//go:build integration

package integration_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	mockpayhandler "bike-enroll/server/internal/handler/mockpay"
	"bike-enroll/server/internal/model"
	paymentpkg "bike-enroll/server/internal/pkg/payment"
	paymentrepo "bike-enroll/server/internal/repository/payment"
	"bike-enroll/server/internal/router"
	paymentsvc "bike-enroll/server/internal/service/payment"
)

func markOrderPaidForRefund(t *testing.T, env *testEnv, orderID, enrollmentID string) {
	t.Helper()
	now := time.Now()
	if err := env.db.Model(&model.Order{}).
		Where("id = ?", orderID).
		Updates(map[string]any{
			"status":         "paid",
			"paid_at":        now,
			"transaction_id": "wx-refund-pay-" + orderID,
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

func newMockRefundEngine(t *testing.T, env *testEnv) http.Handler {
	t.Helper()
	service := paymentsvc.New(paymentrepo.NewGORM(env.db), testLogger())
	service.SetProvider(paymentpkg.NewMockProvider())
	service.SetRefundRepository(paymentrepo.NewGORMRefund(env.db, env.rdb))
	handler := mockpayhandler.New(service, mockPayTestSecret, testLogger())

	gin.SetMode(gin.TestMode)
	return router.New(testLogger(), router.WithMockPay(handler, true))
}

func postMockRefund(engine http.Handler, orderID, action, secret, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/internal/mock-refund/"+orderID+"/"+action, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if secret != "" {
		req.Header.Set("X-Mock-Secret", secret)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

type refundApplyResponse struct {
	Code        string `json:"code"`
	OrderID     string `json:"order_id"`
	OutRefundNo string `json:"out_refund_no"`
	RefundID    string `json:"refund_id"`
	Status      string `json:"status"`
}

func decodeRefundApply(t *testing.T, rec *httptest.ResponseRecorder) refundApplyResponse {
	t.Helper()
	var body refundApplyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v: %s", err, rec.Body.String())
	}
	return body
}

func countRefunds(t *testing.T, env *testEnv, outRefundNo string) int64 {
	t.Helper()
	var count int64
	if err := env.db.Model(&model.Refund{}).Where("out_refund_no = ?", outRefundNo).Count(&count).Error; err != nil {
		t.Fatalf("统计退款记录失败: %v", err)
	}
	return count
}

func loadRefund(t *testing.T, env *testEnv, outRefundNo string) model.Refund {
	t.Helper()
	var refund model.Refund
	if err := env.db.Where("out_refund_no = ?", outRefundNo).First(&refund).Error; err != nil {
		t.Fatalf("读取退款记录失败: %v", err)
	}
	return refund
}

func TestMockRefundApplyThenSuccess(t *testing.T) {
	env := newEnv(t)
	orderID, enrollmentID, groupID := seedMockPayOrder(t, env, 10000)
	t.Cleanup(func() { env.db.Where("order_id = ?", orderID).Delete(&model.Refund{}) })
	markOrderPaidForRefund(t, env, orderID, enrollmentID)
	engine := newMockRefundEngine(t, env)

	apply := postMockRefund(engine, orderID, "apply", mockPayTestSecret, `{"out_refund_no":"refund-1"}`)
	if apply.Code != http.StatusOK {
		t.Fatalf("apply status = %d: %s", apply.Code, apply.Body.String())
	}
	order := loadOrder(t, env, orderID)
	if order.Status != "refunding" {
		t.Errorf("apply 后 Order.status = %q，期望 refunding", order.Status)
	}
	if remaining := redisInt(t, env, "group:"+groupID+":remaining"); remaining != 4 {
		t.Errorf("apply 不应修改 Redis：remaining=%d，期望 4", remaining)
	}

	success := postMockRefund(engine, orderID, "success", mockPayTestSecret, "")
	if success.Code != http.StatusOK {
		t.Fatalf("success status = %d: %s", success.Code, success.Body.String())
	}
	order = loadOrder(t, env, orderID)
	if order.Status != "refunded" {
		t.Errorf("success 后 Order.status = %q，期望 refunded", order.Status)
	}
	enrollment := loadEnrollment(t, env, enrollmentID)
	if enrollment.Status != "refunded" {
		t.Errorf("success 后 Enrollment.status = %q，期望 refunded", enrollment.Status)
	}
	if remaining := redisInt(t, env, "group:"+groupID+":remaining"); remaining != 5 {
		t.Errorf("退款成功后 Redis remaining = %d，期望 5", remaining)
	}
	if refund := loadRefund(t, env, "refund-1"); refund.Status != "SUCCESS" {
		t.Errorf("退款记录 status = %q，期望 SUCCESS", refund.Status)
	}
}

func TestMockRefundApplyThenClosed(t *testing.T) {
	env := newEnv(t)
	orderID, enrollmentID, groupID := seedMockPayOrder(t, env, 10000)
	t.Cleanup(func() { env.db.Where("order_id = ?", orderID).Delete(&model.Refund{}) })
	markOrderPaidForRefund(t, env, orderID, enrollmentID)
	engine := newMockRefundEngine(t, env)

	if rec := postMockRefund(engine, orderID, "apply", mockPayTestSecret, `{"out_refund_no":"refund-2"}`); rec.Code != http.StatusOK {
		t.Fatalf("apply status = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := postMockRefund(engine, orderID, "closed", mockPayTestSecret, ""); rec.Code != http.StatusOK {
		t.Fatalf("closed status = %d: %s", rec.Code, rec.Body.String())
	}

	order := loadOrder(t, env, orderID)
	if order.Status != "refund_failed" {
		t.Errorf("closed 后 Order.status = %q，期望 refund_failed", order.Status)
	}
	enrollment := loadEnrollment(t, env, enrollmentID)
	if enrollment.Status != "paid" {
		t.Errorf("closed 后 Enrollment.status = %q，期望保持 paid", enrollment.Status)
	}
	if remaining := redisInt(t, env, "group:"+groupID+":remaining"); remaining != 4 {
		t.Errorf("closed 不应回补 Redis：remaining=%d，期望 4", remaining)
	}
	if refund := loadRefund(t, env, "refund-2"); refund.Status != "CLOSED" {
		t.Errorf("退款记录 status = %q，期望 CLOSED", refund.Status)
	}
}

func TestMockRefundSuccessTwiceIsIdempotent(t *testing.T) {
	env := newEnv(t)
	orderID, enrollmentID, groupID := seedMockPayOrder(t, env, 10000)
	t.Cleanup(func() { env.db.Where("order_id = ?", orderID).Delete(&model.Refund{}) })
	markOrderPaidForRefund(t, env, orderID, enrollmentID)
	engine := newMockRefundEngine(t, env)

	if rec := postMockRefund(engine, orderID, "apply", mockPayTestSecret, `{"out_refund_no":"refund-3"}`); rec.Code != http.StatusOK {
		t.Fatalf("apply status = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := postMockRefund(engine, orderID, "success", mockPayTestSecret, ""); rec.Code != http.StatusOK {
		t.Fatalf("第一次 success status = %d: %s", rec.Code, rec.Body.String())
	}
	if remaining := redisInt(t, env, "group:"+groupID+":remaining"); remaining != 5 {
		t.Fatalf("第一次退款后 Redis remaining = %d，期望 5", remaining)
	}

	if rec := postMockRefund(engine, orderID, "success", mockPayTestSecret, ""); rec.Code != http.StatusOK {
		t.Fatalf("第二次 success status = %d: %s", rec.Code, rec.Body.String())
	}
	if remaining := redisInt(t, env, "group:"+groupID+":remaining"); remaining != 5 {
		t.Errorf("重复 success 后 Redis remaining = %d，期望仍为 5", remaining)
	}
	if count := countRefunds(t, env, "refund-3"); count != 1 {
		t.Errorf("退款记录数 = %d，期望 1", count)
	}
	order := loadOrder(t, env, orderID)
	if order.Status != "refunded" {
		t.Errorf("Order.status = %q，期望 refunded", order.Status)
	}
	_ = enrollmentID
}

func TestMockRefundRepeatedApplySameOutRefundNo(t *testing.T) {
	env := newEnv(t)
	orderID, enrollmentID, groupID := seedMockPayOrder(t, env, 10000)
	t.Cleanup(func() { env.db.Where("order_id = ?", orderID).Delete(&model.Refund{}) })
	markOrderPaidForRefund(t, env, orderID, enrollmentID)
	engine := newMockRefundEngine(t, env)

	first := postMockRefund(engine, orderID, "apply", mockPayTestSecret, `{"out_refund_no":"refund-4"}`)
	if first.Code != http.StatusOK {
		t.Fatalf("第一次 apply status = %d: %s", first.Code, first.Body.String())
	}
	firstBody := decodeRefundApply(t, first)

	second := postMockRefund(engine, orderID, "apply", mockPayTestSecret, `{"out_refund_no":"refund-4"}`)
	if second.Code != http.StatusOK {
		t.Fatalf("第二次 apply status = %d: %s", second.Code, second.Body.String())
	}
	secondBody := decodeRefundApply(t, second)

	if firstBody.RefundID != secondBody.RefundID || firstBody.Status != secondBody.Status ||
		firstBody.OutRefundNo != secondBody.OutRefundNo {
		t.Errorf("重复 apply 返回不一致: first=%+v second=%+v", firstBody, secondBody)
	}
	if count := countRefunds(t, env, "refund-4"); count != 1 {
		t.Errorf("重复 apply 产生了 %d 条退款记录，期望 1", count)
	}
	if order := loadOrder(t, env, orderID); order.Status != "refunding" {
		t.Errorf("apply 后 Order.status = %q，期望 refunding", order.Status)
	}
	if remaining := redisInt(t, env, "group:"+groupID+":remaining"); remaining != 4 {
		t.Errorf("apply 不应修改 Redis：remaining=%d，期望 4", remaining)
	}
}

func TestMockRefundRequiresSecret(t *testing.T) {
	env := newEnv(t)
	orderID, enrollmentID, _ := seedMockPayOrder(t, env, 10000)
	t.Cleanup(func() { env.db.Where("order_id = ?", orderID).Delete(&model.Refund{}) })
	markOrderPaidForRefund(t, env, orderID, enrollmentID)
	engine := newMockRefundEngine(t, env)

	for _, secret := range []string{"", "wrong-secret"} {
		rec := postMockRefund(engine, orderID, "apply", secret, `{"out_refund_no":"refund-5"}`)
		if rec.Code != http.StatusForbidden {
			t.Errorf("secret=%q status = %d，期望 403", secret, rec.Code)
		}
	}
	if order := loadOrder(t, env, orderID); order.Status != "paid" {
		t.Errorf("未授权 apply 不应修改订单: %s", order.Status)
	}
}

func TestMockRefundRoutesDisabledOutsideMockMode(t *testing.T) {
	env := newEnv(t)
	orderID, _, _ := seedMockPayOrder(t, env, 10000)
	service := paymentsvc.New(paymentrepo.NewGORM(env.db), testLogger())
	service.SetProvider(paymentpkg.NewMockProvider())
	service.SetRefundRepository(paymentrepo.NewGORMRefund(env.db, env.rdb))
	handler := mockpayhandler.New(service, mockPayTestSecret, testLogger())

	gin.SetMode(gin.TestMode)
	engine := router.New(testLogger(), router.WithMockPay(handler, false))
	rec := postMockRefund(engine, orderID, "apply", mockPayTestSecret, `{"out_refund_no":"refund-6"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("mock 退款路由未启用时 status = %d，期望 404", rec.Code)
	}
}
