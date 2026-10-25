package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// MockProvider 是本地联调用的支付提供者。
//
// 它不访问微信支付，也不校验回调签名；除此之外，
// Service.HandleNotify 的状态迁移逻辑与 wechat 模式完全一致。
type MockProvider struct{}

// NewMockProvider 创建 mock 支付提供者。
func NewMockProvider() *MockProvider {
	return &MockProvider{}
}

// Prepay 返回固定格式的假支付参数。
//
// package 中带 out_trade_no，便于在小程序日志里核对订单；
// 这些参数不能用于真实微信支付。
func (p *MockProvider) Prepay(_ context.Context, req PrepayRequest) (*PrepayResult, error) {
	if req.OutTradeNo == "" {
		return nil, fmt.Errorf("%w: out_trade_no is required", ErrInvalidRequest)
	}

	prepayID := "mock_prepay_" + req.OutTradeNo
	return &PrepayResult{
		PrepayID:  prepayID,
		TimeStamp: strconv.FormatInt(time.Now().Unix(), 10),
		NonceStr:  "mocknonce",
		Package:   "prepay_id=" + prepayID,
		SignType:  "RSA",
		PaySign:   "mock-pay-sign",
	}, nil
}

// VerifyNotify 只做 json.Unmarshal，不验签。
//
// 请求体直接使用 NotifyResult 的 JSON 结构，例如：
//
//	{"event_type":"TRANSACTION.SUCCESS","out_trade_no":"...","transaction_id":"...",
//	 "trade_state":"SUCCESS","amount":{"total":1,"currency":"CNY"}}
func (p *MockProvider) VerifyNotify(_ context.Context, _ http.Header, body []byte) (*NotifyResult, error) {
	var result NotifyResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	return &result, nil
}

// Refund 返回固定的 PROCESSING 假结果，不访问微信。
func (p *MockProvider) Refund(_ context.Context, req RefundRequest) (*RefundResult, error) {
	if req.OutRefundNo == "" {
		return nil, fmt.Errorf("%w: out_refund_no is required", ErrInvalidRequest)
	}
	return &RefundResult{
		Status:   RefundStatusProcessing,
		RefundID: "mock_refund_" + req.OutRefundNo,
	}, nil
}

// VerifyRefundNotify 只做 json.Unmarshal，不验签。
func (p *MockProvider) VerifyRefundNotify(_ context.Context, _ http.Header, body []byte) (*RefundNotifyResult, error) {
	var result RefundNotifyResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	return &result, nil
}
