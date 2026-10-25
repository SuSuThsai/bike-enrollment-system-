//go:build integration

package integration_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	mockpayhandler "bike-enroll/server/internal/handler/mockpay"
	"bike-enroll/server/internal/model"
	paymentrepo "bike-enroll/server/internal/repository/payment"
	"bike-enroll/server/internal/router"
	paymentsvc "bike-enroll/server/internal/service/payment"
)

const mockPayTestSecret = "mock-pay-test-secret"

// seedMockPayOrder 创建 user + pending order + pending enrollment，Redis 名额置 4。
func seedMockPayOrder(t *testing.T, env *testEnv, amount int) (orderID, enrollmentID, groupID string) {
	t.Helper()

	now := time.Now()
	userID := uuid.NewString()
	groupID = uuid.NewString()
	enrollmentID = uuid.NewString()
	orderID = uuid.NewString()

	user := &model.User{
		ID:        userID,
		OpenID:    "mock-openid-" + userID,
		Nickname:  "Mock Payer",
		Role:      "rider",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := env.db.Create(user).Error; err != nil {
		t.Fatalf("创建 User 失败: %v", err)
	}

	enrollment := &model.Enrollment{
		ID:        enrollmentID,
		UserID:    userID,
		EventID:   uuid.NewString(),
		GroupID:   groupID,
		OrderID:   orderID,
		Status:    "pending_payment",
		FormData:  map[string]any{"name": "mock pay test"},
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

	redisKey := "group:" + groupID + ":remaining"
	if err := env.rdb.Set(t.Context(), redisKey, 4, 0).Err(); err != nil {
		t.Fatalf("初始化 Redis 名额失败: %v", err)
	}

	t.Cleanup(func() {
		env.db.Where("id = ?", orderID).Delete(&model.Order{})
		env.db.Where("id = ?", enrollmentID).Delete(&model.Enrollment{})
		env.db.Where("id = ?", userID).Delete(&model.User{})
		env.rdb.Del(t.Context(), redisKey)
	})

	return orderID, enrollmentID, groupID
}

func newMockPayEngine(t *testing.T, service *paymentsvc.Service, secret string, enabled bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	handler := mockpayhandler.New(service, secret, testLogger())
	return router.New(testLogger(), router.WithMockPay(handler, enabled))
}

func doMockPay(engine http.Handler, orderID, action, secret string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/internal/mock-pay/"+orderID+"/"+action, nil)
	if secret != "" {
		req.Header.Set("X-Mock-Secret", secret)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func TestMockPaySuccessMarksOrderPaid(t *testing.T) {
	env := newEnv(t)
	orderID, enrollmentID, groupID := seedMockPayOrder(t, env, 100)
	service := paymentsvc.New(paymentrepo.NewGORM(env.db), testLogger())
	engine := newMockPayEngine(t, service, mockPayTestSecret, true)

	rec := doMockPay(engine, orderID, "success", mockPayTestSecret)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d，期望 200: %s", rec.Code, rec.Body.String())
	}

	order := loadOrder(t, env, orderID)
	if order.Status != "paid" || order.PaidAt == nil {
		t.Errorf("Order 未迁移为 paid: %+v", order)
	}
	if order.TransactionID == nil || *order.TransactionID == "" {
		t.Errorf("Order.transaction_id 为空")
	}
	enrollment := loadEnrollment(t, env, enrollmentID)
	if enrollment.Status != "paid" || enrollment.PaidAt == nil {
		t.Errorf("Enrollment 未迁移为 paid: %+v", enrollment)
	}
	if remaining := redisInt(t, env, "group:"+groupID+":remaining"); remaining != 4 {
		t.Errorf("Mock 支付不应修改 Redis: remaining=%d，期望 4", remaining)
	}
}

func TestMockPaySuccessIsIdempotent(t *testing.T) {
	env := newEnv(t)
	orderID, enrollmentID, groupID := seedMockPayOrder(t, env, 100)
	service := paymentsvc.New(paymentrepo.NewGORM(env.db), testLogger())
	engine := newMockPayEngine(t, service, mockPayTestSecret, true)

	if rec := doMockPay(engine, orderID, "success", mockPayTestSecret); rec.Code != http.StatusOK {
		t.Fatalf("第一次 mock success status = %d: %s", rec.Code, rec.Body.String())
	}
	firstOrder := loadOrder(t, env, orderID)
	firstEnrollment := loadEnrollment(t, env, enrollmentID)
	if firstOrder.PaidAt == nil || firstOrder.TransactionID == nil {
		t.Fatalf("第一次 mock success 未完成支付: %+v", firstOrder)
	}

	if rec := doMockPay(engine, orderID, "success", mockPayTestSecret); rec.Code != http.StatusOK {
		t.Fatalf("第二次 mock success status = %d: %s", rec.Code, rec.Body.String())
	}
	secondOrder := loadOrder(t, env, orderID)
	secondEnrollment := loadEnrollment(t, env, enrollmentID)

	if secondOrder.Status != "paid" || secondEnrollment.Status != "paid" {
		t.Errorf("重复触发后状态异常: order=%s enrollment=%s", secondOrder.Status, secondEnrollment.Status)
	}
	if secondOrder.TransactionID == nil || *secondOrder.TransactionID != *firstOrder.TransactionID {
		t.Errorf("重复触发不应改变 transaction_id: %v -> %v", firstOrder.TransactionID, secondOrder.TransactionID)
	}
	if secondOrder.PaidAt == nil || !secondOrder.PaidAt.Equal(*firstOrder.PaidAt) {
		t.Errorf("重复触发不应改变 Order.paid_at")
	}
	if !secondOrder.UpdatedAt.Equal(firstOrder.UpdatedAt) {
		t.Errorf("重复触发不应改变 Order.updated_at")
	}
	if secondEnrollment.PaidAt == nil || !secondEnrollment.PaidAt.Equal(*firstEnrollment.PaidAt) {
		t.Errorf("重复触发不应改变 Enrollment.paid_at")
	}
	if remaining := redisInt(t, env, "group:"+groupID+":remaining"); remaining != 4 {
		t.Errorf("重复触发不应改变 Redis: remaining=%d，期望 4", remaining)
	}
}

func TestMockPayFailKeepsPendingAndLogsAlert(t *testing.T) {
	env := newEnv(t)
	orderID, enrollmentID, groupID := seedMockPayOrder(t, env, 100)

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	service := paymentsvc.New(paymentrepo.NewGORM(env.db), logger)
	engine := newMockPayEngine(t, service, mockPayTestSecret, true)

	rec := doMockPay(engine, orderID, "fail", mockPayTestSecret)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d，期望 200: %s", rec.Code, rec.Body.String())
	}

	order := loadOrder(t, env, orderID)
	if order.Status != "pending_payment" {
		t.Errorf("金额不符后 Order.status = %q，期望 pending_payment", order.Status)
	}
	if order.PaidAt != nil || order.TransactionID != nil {
		t.Errorf("金额不符不应写入 paid_at/transaction_id: %+v", order)
	}
	enrollment := loadEnrollment(t, env, enrollmentID)
	if enrollment.Status != "pending_payment" || enrollment.PaidAt != nil {
		t.Errorf("金额不符后 Enrollment 状态异常: %+v", enrollment)
	}
	if remaining := redisInt(t, env, "group:"+groupID+":remaining"); remaining != 4 {
		t.Errorf("金额不符不应修改 Redis: remaining=%d，期望 4", remaining)
	}
	if !bytes.Contains(logs.Bytes(), []byte("PAY_AMOUNT_MISMATCH")) {
		t.Errorf("日志缺少 PAY_AMOUNT_MISMATCH 告警: %s", logs.String())
	}
}

func TestMockPayRequiresSecret(t *testing.T) {
	env := newEnv(t)
	orderID, _, _ := seedMockPayOrder(t, env, 100)
	service := paymentsvc.New(paymentrepo.NewGORM(env.db), testLogger())
	engine := newMockPayEngine(t, service, mockPayTestSecret, true)

	for _, secret := range []string{"", "wrong-secret"} {
		rec := doMockPay(engine, orderID, "success", secret)
		if rec.Code != http.StatusForbidden {
			t.Errorf("secret=%q status = %d，期望 403: %s", secret, rec.Code, rec.Body.String())
		}
	}

	order := loadOrder(t, env, orderID)
	if order.Status != "pending_payment" {
		t.Errorf("未授权请求不应修改订单状态: %s", order.Status)
	}
}

func TestMockPayRoutesDisabledOutsideMockMode(t *testing.T) {
	env := newEnv(t)
	orderID, _, _ := seedMockPayOrder(t, env, 100)
	service := paymentsvc.New(paymentrepo.NewGORM(env.db), testLogger())
	engine := newMockPayEngine(t, service, mockPayTestSecret, false)

	rec := doMockPay(engine, orderID, "success", mockPayTestSecret)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("PAYMENT_MODE!=mock 时 status = %d，期望 404: %s", rec.Code, rec.Body.String())
	}
}
