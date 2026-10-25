// Package payment 定义支付网关提供者抽象。
//
// 上层 handler/service 只依赖 Provider 接口，不感知 mock / wechat 的差异；
// 具体实现由 factory.go 根据 PAYMENT_MODE 选择。
package payment

import (
	"context"
	"errors"
	"net/http"
)

// Provider 是支付网关抽象：预下单、支付回调、申请退款、退款回调。
type Provider interface {
	// Prepay 发起预下单并返回小程序支付参数。
	Prepay(ctx context.Context, req PrepayRequest) (*PrepayResult, error)

	// VerifyNotify 校验支付回调来源并解析出支付结果。
	//
	// mock 实现只做 json.Unmarshal，不验签；wechat 实现先平台证书验签再解密。
	VerifyNotify(ctx context.Context, headers http.Header, body []byte) (*NotifyResult, error)

	// Refund 向支付网关申请退款。
	Refund(ctx context.Context, req RefundRequest) (*RefundResult, error)

	// VerifyRefundNotify 校验退款回调来源并解析退款结果。
	//
	// mock 实现只做 json.Unmarshal，不验签；wechat 实现先平台证书验签再解密。
	VerifyRefundNotify(ctx context.Context, headers http.Header, body []byte) (*RefundNotifyResult, error)
}

// 支付提供者错误，handler 据此映射 HTTP 状态码。
var (
	// ErrNotConfigured 表示该 Provider 的必备配置缺失，无法发起请求。
	ErrNotConfigured = errors.New("payment provider not configured")

	// ErrInvalidSignature 表示回调验签失败。
	ErrInvalidSignature = errors.New("payment notify signature invalid")

	// ErrInvalidPayload 表示回调报文无法解析或解密。
	ErrInvalidPayload = errors.New("payment notify payload invalid")

	// ErrInvalidRequest 表示预下单参数不合法。
	ErrInvalidRequest = errors.New("invalid payment prepay request")
)

// PrepayRequest 是与网关无关的预下单参数。
type PrepayRequest struct {
	// OutTradeNo 是商户订单号，对应 Order.id。
	OutTradeNo string
	// Description 是商品描述。
	Description string
	// AmountTotal 是订单金额，单位分。
	AmountTotal int
	// OpenID 是微信小程序用户标识。
	OpenID string
}

// PrepayResult 是小程序 wx.requestPayment 所需参数。
//
// 字段名与 docs/openapi.yaml 的 PrepayResponse 保持一致；
// PrepayID 仅用于服务端日志/排查，不返回给客户端。
type PrepayResult struct {
	PrepayID  string `json:"-"`
	TimeStamp string `json:"timeStamp"`
	NonceStr  string `json:"nonceStr"`
	Package   string `json:"package"`
	SignType  string `json:"signType"`
	PaySign   string `json:"paySign"`
}

// NotifyResult 是回调解析后的支付结果。
//
// 字段与微信支付 V3 resource 解密后的明文保持一致；
// mock 模式的回调 JSON 也使用同一结构，保证 Service.HandleNotify 走同一套状态迁移。
type NotifyResult struct {
	// EventType 是回调事件类型，MVP 只处理 TRANSACTION.SUCCESS。
	EventType string `json:"event_type"`
	// OutTradeNo 是商户订单号，对应 Order.id。
	OutTradeNo string `json:"out_trade_no"`
	// TransactionID 是微信支付订单号，回调幂等键。
	TransactionID string `json:"transaction_id"`
	// TradeState 是交易状态，只处理 SUCCESS。
	TradeState string `json:"trade_state"`
	// SuccessTime 是支付成功时间（RFC3339 字符串）。
	SuccessTime string `json:"success_time"`
	// Amount 是金额信息，单位分。
	Amount NotifyAmount `json:"amount"`
}

// NotifyAmount 是回调金额。
type NotifyAmount struct {
	Total    int    `json:"total"`
	Currency string `json:"currency"`
}

// 退款状态常量。RefundResult.Status 只会是 PROCESSING 或 SUCCESS；
// 退款回调的 RefundStatus 还可能是 CLOSED / ABNORMAL。
const (
	RefundStatusProcessing = "PROCESSING"
	RefundStatusSuccess    = "SUCCESS"
	RefundStatusClosed     = "CLOSED"
	RefundStatusAbnormal   = "ABNORMAL"
)

// RefundRequest 是与网关无关的退款申请参数。
//
// MVP 按全额退款处理：Amount 同时作为退款金额与订单总额。
type RefundRequest struct {
	// OutTradeNo 是商户订单号，对应 Order.id。
	OutTradeNo string
	// OutRefundNo 是商户退款单号，用于退款幂等。
	OutRefundNo string
	// Amount 是退款金额，单位分。
	Amount int
}

// RefundResult 是退款申请结果。
type RefundResult struct {
	// Status 为 PROCESSING 或 SUCCESS。
	Status string
	// RefundID 是支付平台退款单号。
	RefundID string
}

// RefundNotifyResult 是退款回调解析后的结果。
//
// OutTradeNo 不在需求最初列出的四个字段中，但退款回调必须能定位 Order；
// WeChat V3 退款 resource 明文里包含 out_trade_no，因此这里保留该字段。
type RefundNotifyResult struct {
	// OutTradeNo 是商户订单号，对应 Order.id。
	OutTradeNo string `json:"out_trade_no"`
	// OutRefundNo 是商户退款单号。
	OutRefundNo string `json:"out_refund_no"`
	// RefundID 是支付平台退款单号。
	RefundID string `json:"refund_id"`
	// RefundStatus 为 SUCCESS / CLOSED / ABNORMAL。
	RefundStatus string `json:"refund_status"`
	// Amount 是退款金额，单位分。
	Amount int `json:"amount"`
}
