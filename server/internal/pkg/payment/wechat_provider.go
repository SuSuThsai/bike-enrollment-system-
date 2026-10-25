package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	paywechat "bike-enroll/server/internal/pkg/wechat/pay"
)

// WeChatConfig 是微信支付 V3 提供者配置。
type WeChatConfig struct {
	AppID          string
	MchID          string
	SerialNo       string
	NotifyURL      string
	PrivateKeyPath string
	APIv3Key       string
	// PlatformCertPath 是微信支付平台证书路径，回调验签使用。
	PlatformCertPath string
	// BaseURL 仅用于本地桩/联调；为空时使用微信官方地址。
	BaseURL string
}

// WeChatProvider 是微信支付 V3 的 Provider 实现。
type WeChatProvider struct {
	appID     string
	mchID     string
	notifyURL string

	client    *paywechat.Client
	clientErr error

	decoder    *paywechat.NotifyDecoder
	decoderErr error
}

// NewWeChatProvider 创建微信支付 Provider。
//
// 配置不完整时不阻断进程启动：对应方法返回 ErrNotConfigured，
// 与改造前“服务可启动、prepay 返回 500”的行为保持一致。
func NewWeChatProvider(cfg WeChatConfig, logger *slog.Logger) *WeChatProvider {
	if logger == nil {
		logger = slog.Default()
	}

	provider := &WeChatProvider{
		appID:     cfg.AppID,
		mchID:     cfg.MchID,
		notifyURL: cfg.NotifyURL,
	}

	provider.client, provider.clientErr = paywechat.NewClient(paywechat.Config{
		AppID:          cfg.AppID,
		MchID:          cfg.MchID,
		SerialNo:       cfg.SerialNo,
		NotifyURL:      cfg.NotifyURL,
		PrivateKeyPath: cfg.PrivateKeyPath,
		BaseURL:        cfg.BaseURL,
	})
	if provider.clientErr != nil {
		logger.Warn("wechat pay client not configured; prepay will fail", "error", provider.clientErr)
	}

	provider.decoder, provider.decoderErr = paywechat.NewNotifyDecoder(paywechat.NotifyConfig{
		PlatformCertPath: cfg.PlatformCertPath,
		APIv3Key:         cfg.APIv3Key,
	})
	if provider.decoderErr != nil {
		logger.Warn("wechat pay notify decoder not configured; notify will fail", "error", provider.decoderErr)
	}

	return provider
}

// Prepay 调用微信支付 V3 JSAPI 统一下单，并生成小程序支付参数。
func (p *WeChatProvider) Prepay(ctx context.Context, req PrepayRequest) (*PrepayResult, error) {
	if p.client == nil {
		return nil, fmt.Errorf("%w: %v", ErrNotConfigured, p.clientErr)
	}
	if req.OutTradeNo == "" || req.AmountTotal <= 0 {
		return nil, ErrInvalidRequest
	}

	prepayID, err := p.client.Prepay(ctx, paywechat.PrepayRequest{
		AppID:       p.appID,
		MchID:       p.mchID,
		Description: req.Description,
		OutTradeNo:  req.OutTradeNo,
		NotifyURL:   p.notifyURL,
		Amount: paywechat.Amount{
			Total:    req.AmountTotal,
			Currency: "CNY",
		},
		Payer: paywechat.Payer{OpenID: req.OpenID},
	})
	if err != nil {
		return nil, fmt.Errorf("wechat pay prepay: %w", err)
	}

	params, err := p.client.BuildPayParams(prepayID, time.Now())
	if err != nil {
		return nil, fmt.Errorf("build wechat pay params: %w", err)
	}

	return &PrepayResult{
		PrepayID:  prepayID,
		TimeStamp: params.TimeStamp,
		NonceStr:  params.NonceStr,
		Package:   params.Package,
		SignType:  params.SignType,
		PaySign:   params.PaySign,
	}, nil
}

// VerifyNotify 先做平台证书验签，再解密 resource 并映射为 NotifyResult。
func (p *WeChatProvider) VerifyNotify(_ context.Context, headers http.Header, body []byte) (*NotifyResult, error) {
	if p.decoder == nil {
		return nil, fmt.Errorf("%w: %v", ErrNotConfigured, p.decoderErr)
	}

	if err := p.decoder.Verify(headers, body); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}

	var envelope paywechat.NotifyEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}

	// 非支付成功事件只返回事件类型，交由 Service.HandleNotify 记录日志后返回 200。
	if envelope.EventType != "TRANSACTION.SUCCESS" {
		return &NotifyResult{EventType: envelope.EventType}, nil
	}

	transaction, err := p.decoder.Decrypt(envelope.Resource)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}

	return &NotifyResult{
		EventType:     envelope.EventType,
		OutTradeNo:    transaction.OutTradeNo,
		TransactionID: transaction.TransactionID,
		TradeState:    transaction.TradeState,
		SuccessTime:   transaction.SuccessTime,
		Amount: NotifyAmount{
			Total:    transaction.Amount.Total,
			Currency: transaction.Amount.Currency,
		},
	}, nil
}

// Refund 调用微信支付 V3 退款接口。MVP 按全额退款处理。
func (p *WeChatProvider) Refund(ctx context.Context, req RefundRequest) (*RefundResult, error) {
	if p.client == nil {
		return nil, fmt.Errorf("%w: %v", ErrNotConfigured, p.clientErr)
	}
	if req.OutTradeNo == "" || req.OutRefundNo == "" || req.Amount <= 0 {
		return nil, ErrInvalidRequest
	}

	response, err := p.client.Refund(ctx, paywechat.RefundRequest{
		OutTradeNo:  req.OutTradeNo,
		OutRefundNo: req.OutRefundNo,
		Amount: paywechat.RefundAmount{
			Refund:   req.Amount,
			Total:    req.Amount,
			Currency: "CNY",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("wechat pay refund: %w", err)
	}

	var status string
	switch response.Status {
	case paywechat.RefundStatusSuccess:
		status = RefundStatusSuccess
	case paywechat.RefundStatusProcessing:
		status = RefundStatusProcessing
	default:
		return nil, fmt.Errorf("unexpected wechat refund status %q", response.Status)
	}

	return &RefundResult{Status: status, RefundID: response.RefundID}, nil
}

// VerifyRefundNotify 先平台证书验签，再解密退款 resource。
func (p *WeChatProvider) VerifyRefundNotify(_ context.Context, headers http.Header, body []byte) (*RefundNotifyResult, error) {
	if p.decoder == nil {
		return nil, fmt.Errorf("%w: %v", ErrNotConfigured, p.decoderErr)
	}

	if err := p.decoder.Verify(headers, body); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}

	var envelope paywechat.NotifyEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	if !strings.HasPrefix(envelope.EventType, "REFUND.") {
		return nil, fmt.Errorf("%w: unexpected refund event_type %q", ErrInvalidPayload, envelope.EventType)
	}

	transaction, err := p.decoder.DecryptRefund(envelope.Resource)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}

	return &RefundNotifyResult{
		OutTradeNo:   transaction.OutTradeNo,
		OutRefundNo:  transaction.OutRefundNo,
		RefundID:     transaction.RefundID,
		RefundStatus: transaction.RefundStatus,
		Amount:       transaction.Amount.Refund,
	}, nil
}
