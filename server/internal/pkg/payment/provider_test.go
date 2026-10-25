package payment_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	paymentpkg "bike-enroll/server/internal/pkg/payment"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
}

func TestMockPrepayReturnsFixedShape(t *testing.T) {
	provider := paymentpkg.NewMockProvider()
	result, err := provider.Prepay(context.Background(), paymentpkg.PrepayRequest{
		OutTradeNo:  "order-1",
		Description: "赛事报名-order-1",
		AmountTotal: 1,
		OpenID:      "openid-1",
	})
	if err != nil {
		t.Fatalf("mock prepay 失败: %v", err)
	}
	if result.PrepayID != "mock_prepay_order-1" {
		t.Errorf("prepay_id = %q", result.PrepayID)
	}
	if result.Package != "prepay_id=mock_prepay_order-1" {
		t.Errorf("package = %q", result.Package)
	}
	if result.SignType != "RSA" || result.PaySign == "" || result.NonceStr == "" || result.TimeStamp == "" {
		t.Errorf("mock 支付参数不完整: %+v", result)
	}
}

func TestMockVerifyNotifyDoesNotRequireSignature(t *testing.T) {
	provider := paymentpkg.NewMockProvider()
	body, err := json.Marshal(paymentpkg.NotifyResult{
		EventType:     "TRANSACTION.SUCCESS",
		OutTradeNo:    "order-1",
		TransactionID: "mock-transaction-1",
		TradeState:    "SUCCESS",
		Amount:        paymentpkg.NotifyAmount{Total: 1, Currency: "CNY"},
	})
	if err != nil {
		t.Fatalf("序列化回调失败: %v", err)
	}

	result, err := provider.VerifyNotify(context.Background(), nil, body)
	if err != nil {
		t.Fatalf("mock VerifyNotify 不应要求签名: %v", err)
	}
	if result.OutTradeNo != "order-1" || result.TransactionID != "mock-transaction-1" ||
		result.TradeState != "SUCCESS" || result.Amount.Total != 1 {
		t.Errorf("mock 回调解析错误: %+v", result)
	}
}

func TestMockVerifyNotifyRejectsInvalidJSON(t *testing.T) {
	provider := paymentpkg.NewMockProvider()
	_, err := provider.VerifyNotify(context.Background(), nil, []byte("not-json"))
	if !errors.Is(err, paymentpkg.ErrInvalidPayload) {
		t.Fatalf("err = %v，期望 ErrInvalidPayload", err)
	}
}

func TestFactoryMockMode(t *testing.T) {
	provider, err := paymentpkg.NewProvider(paymentpkg.Config{Mode: " mock "}, testLogger())
	if err != nil {
		t.Fatalf("factory mock 失败: %v", err)
	}
	if _, ok := provider.(*paymentpkg.MockProvider); !ok {
		t.Fatalf("provider 类型 = %T，期望 *MockProvider", provider)
	}
}

func TestMockRefundReturnsProcessing(t *testing.T) {
	provider := paymentpkg.NewMockProvider()
	result, err := provider.Refund(context.Background(), paymentpkg.RefundRequest{
		OutTradeNo:  "order-1",
		OutRefundNo: "refund-1",
		Amount:      1,
	})
	if err != nil {
		t.Fatalf("mock refund 失败: %v", err)
	}
	if result.Status != paymentpkg.RefundStatusProcessing {
		t.Errorf("status = %q，期望 PROCESSING", result.Status)
	}
	if result.RefundID != "mock_refund_refund-1" {
		t.Errorf("refund_id = %q", result.RefundID)
	}
}

func TestMockVerifyRefundNotifyDoesNotRequireSignature(t *testing.T) {
	provider := paymentpkg.NewMockProvider()
	body, err := json.Marshal(paymentpkg.RefundNotifyResult{
		OutTradeNo:   "order-1",
		OutRefundNo:  "refund-1",
		RefundID:     "mock-refund-1",
		RefundStatus: "SUCCESS",
		Amount:       1,
	})
	if err != nil {
		t.Fatalf("序列化退款回调失败: %v", err)
	}

	result, err := provider.VerifyRefundNotify(context.Background(), nil, body)
	if err != nil {
		t.Fatalf("mock VerifyRefundNotify 不应要求签名: %v", err)
	}
	if result.OutTradeNo != "order-1" || result.OutRefundNo != "refund-1" ||
		result.RefundStatus != "SUCCESS" || result.Amount != 1 {
		t.Errorf("mock 退款回调解析错误: %+v", result)
	}
}

func TestWeChatProviderMissingConfigRefund(t *testing.T) {
	provider := paymentpkg.NewWeChatProvider(paymentpkg.WeChatConfig{}, testLogger())

	_, err := provider.Refund(context.Background(), paymentpkg.RefundRequest{
		OutTradeNo:  "order-1",
		OutRefundNo: "refund-1",
		Amount:      1,
	})
	if !errors.Is(err, paymentpkg.ErrNotConfigured) {
		t.Fatalf("refund err = %v，期望 ErrNotConfigured", err)
	}

	_, err = provider.VerifyRefundNotify(context.Background(), nil, []byte(`{}`))
	if !errors.Is(err, paymentpkg.ErrNotConfigured) {
		t.Fatalf("refund notify err = %v，期望 ErrNotConfigured", err)
	}
}

func TestFactoryRejectsMockInProduction(t *testing.T) {
	for _, appEnv := range []string{"production", "PROD", " production "} {
		_, err := paymentpkg.NewProvider(paymentpkg.Config{
			Mode:   "mock",
			AppEnv: appEnv,
		}, testLogger())
		if err == nil {
			t.Errorf("APP_ENV=%q + PAYMENT_MODE=mock 应拒绝启动", appEnv)
			continue
		}
		if !strings.Contains(err.Error(), "forbidden") {
			t.Errorf("APP_ENV=%q err = %v，期望 forbidden 错误", appEnv, err)
		}
	}
}

func TestFactoryAllowsMockInDevelopment(t *testing.T) {
	provider, err := paymentpkg.NewProvider(paymentpkg.Config{
		Mode:   "mock",
		AppEnv: "development",
	}, testLogger())
	if err != nil {
		t.Fatalf("development + mock 应允许启动: %v", err)
	}
	if _, ok := provider.(*paymentpkg.MockProvider); !ok {
		t.Fatalf("provider 类型 = %T，期望 *MockProvider", provider)
	}
}

func TestFactoryUnsupportedMode(t *testing.T) {
	_, err := paymentpkg.NewProvider(paymentpkg.Config{Mode: "alipay"}, testLogger())
	if err == nil || !strings.Contains(err.Error(), "PAYMENT_MODE") {
		t.Fatalf("err = %v，期望包含 PAYMENT_MODE 的错误", err)
	}
}

func TestWeChatProviderMissingConfig(t *testing.T) {
	provider := paymentpkg.NewWeChatProvider(paymentpkg.WeChatConfig{}, testLogger())

	_, err := provider.Prepay(context.Background(), paymentpkg.PrepayRequest{
		OutTradeNo:  "order-1",
		AmountTotal: 1,
	})
	if !errors.Is(err, paymentpkg.ErrNotConfigured) {
		t.Fatalf("prepay err = %v，期望 ErrNotConfigured", err)
	}

	_, err = provider.VerifyNotify(context.Background(), nil, []byte(`{}`))
	if !errors.Is(err, paymentpkg.ErrNotConfigured) {
		t.Fatalf("notify err = %v，期望 ErrNotConfigured", err)
	}
}
