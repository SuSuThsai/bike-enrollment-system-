package payment_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	paymenthandler "bike-enroll/server/internal/handler/payment"
	"bike-enroll/server/internal/middleware"
	paymentpkg "bike-enroll/server/internal/pkg/payment"
	paymentsvc "bike-enroll/server/internal/service/payment"
)

type fakeProvider struct {
	prepayReq    paymentpkg.PrepayRequest
	prepayCalled bool
	prepayResult *paymentpkg.PrepayResult
	prepayErr    error

	notifyCalled bool
	notifyResult *paymentpkg.NotifyResult
	notifyErr    error
}

func (f *fakeProvider) Prepay(_ context.Context, req paymentpkg.PrepayRequest) (*paymentpkg.PrepayResult, error) {
	f.prepayCalled = true
	f.prepayReq = req
	return f.prepayResult, f.prepayErr
}

func (f *fakeProvider) VerifyNotify(_ context.Context, _ http.Header, _ []byte) (*paymentpkg.NotifyResult, error) {
	f.notifyCalled = true
	return f.notifyResult, f.notifyErr
}

func (f *fakeProvider) Refund(context.Context, paymentpkg.RefundRequest) (*paymentpkg.RefundResult, error) {
	return &paymentpkg.RefundResult{Status: paymentpkg.RefundStatusProcessing, RefundID: "fake-refund"}, nil
}

func (f *fakeProvider) VerifyRefundNotify(context.Context, http.Header, []byte) (*paymentpkg.RefundNotifyResult, error) {
	return &paymentpkg.RefundNotifyResult{}, nil
}

type fakeService struct {
	prepayRequest *paymentpkg.PrepayRequest
	prepayErr     error
	prepareCalled bool

	notifyResult *paymentpkg.NotifyResult
	notifyErr    error
	handleCalled bool
}

func (f *fakeService) PreparePrepay(context.Context, string, string) (*paymentpkg.PrepayRequest, error) {
	f.prepareCalled = true
	return f.prepayRequest, f.prepayErr
}

func (f *fakeService) HandleNotify(_ context.Context, result *paymentpkg.NotifyResult) error {
	f.handleCalled = true
	f.notifyResult = result
	return f.notifyErr
}

func newPrepayRouter(handler *paymenthandler.Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(middleware.ContextUserID, "user-1")
	})
	router.POST("/prepay", handler.Prepay)
	return router
}

func TestPrepayCallsProvider(t *testing.T) {
	service := &fakeService{prepayRequest: &paymentpkg.PrepayRequest{
		OutTradeNo:  "order-1",
		Description: "赛事报名-order-1",
		AmountTotal: 10000,
		OpenID:      "openid-1",
	}}
	provider := &fakeProvider{prepayResult: &paymentpkg.PrepayResult{
		PrepayID:  "prepay-1",
		TimeStamp: "1700000000",
		NonceStr:  "nonce-1",
		Package:   "prepay_id=prepay-1",
		SignType:  "RSA",
		PaySign:   "sign-1",
	}}
	router := newPrepayRouter(paymenthandler.New(provider, service))

	req := httptest.NewRequest(http.MethodPost, "/prepay", strings.NewReader(`{"order_id":"order-1"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d，期望 200: %s", rec.Code, rec.Body.String())
	}
	if !service.prepareCalled || !provider.prepayCalled {
		t.Fatal("prepay 应先调用 Service.PreparePrepay，再调用 Provider.Prepay")
	}
	if provider.prepayReq.OutTradeNo != "order-1" || provider.prepayReq.OpenID != "openid-1" {
		t.Errorf("Provider 收到的预下单参数错误: %+v", provider.prepayReq)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	for key, want := range map[string]string{
		"timeStamp": "1700000000",
		"nonceStr":  "nonce-1",
		"package":   "prepay_id=prepay-1",
		"signType":  "RSA",
		"paySign":   "sign-1",
	} {
		if body[key] != want {
			t.Errorf("%s = %q，期望 %q", key, body[key], want)
		}
	}
	if _, exists := body["prepay_id"]; exists {
		t.Error("prepay_id 不应返回给客户端")
	}
}

func TestPrepayOrderNotFound(t *testing.T) {
	service := &fakeService{prepayErr: paymentsvc.ErrOrderNotFound}
	provider := &fakeProvider{}
	router := newPrepayRouter(paymenthandler.New(provider, service))

	req := httptest.NewRequest(http.MethodPost, "/prepay", strings.NewReader(`{"order_id":"missing"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d，期望 404: %s", rec.Code, rec.Body.String())
	}
	if provider.prepayCalled {
		t.Error("订单校验失败时不应调用 Provider.Prepay")
	}
}

func TestPrepayProviderFailure(t *testing.T) {
	service := &fakeService{prepayRequest: &paymentpkg.PrepayRequest{OutTradeNo: "order-1"}}
	provider := &fakeProvider{prepayErr: errors.New("wechat unavailable")}
	router := newPrepayRouter(paymenthandler.New(provider, service))

	req := httptest.NewRequest(http.MethodPost, "/prepay", strings.NewReader(`{"order_id":"order-1"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d，期望 500: %s", rec.Code, rec.Body.String())
	}
}

func TestNotifySuccessResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	notifyResult := &paymentpkg.NotifyResult{
		EventType:     "TRANSACTION.SUCCESS",
		OutTradeNo:    "order-1",
		TransactionID: "wx-transaction-1",
		TradeState:    "SUCCESS",
		Amount:        paymentpkg.NotifyAmount{Total: 10000, Currency: "CNY"},
	}
	service := &fakeService{}
	handler := paymenthandler.New(&fakeProvider{notifyResult: notifyResult}, service)
	router := gin.New()
	router.POST("/notify", handler.Notify)

	req := httptest.NewRequest(http.MethodPost, "/notify", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d，期望 200: %s", rec.Code, rec.Body.String())
	}
	if !service.handleCalled {
		t.Fatal("VerifyNotify 成功后必须调用 Service.HandleNotify")
	}
	if service.notifyResult != notifyResult {
		t.Error("HandleNotify 未收到 Provider 解析出的结果")
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body["code"] != "SUCCESS" {
		t.Errorf("响应 code = %q，期望 SUCCESS", body["code"])
	}
}

func TestNotifySignInvalidResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &fakeService{}
	handler := paymenthandler.New(&fakeProvider{notifyErr: paymentpkg.ErrInvalidSignature}, service)
	router := gin.New()
	router.POST("/notify", handler.Notify)

	req := httptest.NewRequest(http.MethodPost, "/notify", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d，期望 401", rec.Code)
	}
	if service.handleCalled {
		t.Error("验签失败时不应调用 HandleNotify")
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body["code"] != "PAY_SIGN_INVALID" {
		t.Errorf("响应 code = %q，期望 PAY_SIGN_INVALID", body["code"])
	}
}

func TestNotifyPayloadInvalidResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &fakeService{}
	handler := paymenthandler.New(&fakeProvider{notifyErr: paymentpkg.ErrInvalidPayload}, service)
	router := gin.New()
	router.POST("/notify", handler.Notify)

	req := httptest.NewRequest(http.MethodPost, "/notify", strings.NewReader(`not-json`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d，期望 400", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body["code"] != "INVALID_ARGUMENT" {
		t.Errorf("响应 code = %q，期望 INVALID_ARGUMENT", body["code"])
	}
}

func TestNotifyHandleFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &fakeService{notifyErr: errors.New("db unavailable")}
	handler := paymenthandler.New(&fakeProvider{notifyResult: &paymentpkg.NotifyResult{
		EventType:  "TRANSACTION.SUCCESS",
		OutTradeNo: "order-1",
	}}, service)
	router := gin.New()
	router.POST("/notify", handler.Notify)

	req := httptest.NewRequest(http.MethodPost, "/notify", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d，期望 500", rec.Code)
	}
}
