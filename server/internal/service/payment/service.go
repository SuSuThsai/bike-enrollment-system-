// Package payment 实现微信支付预下单与回调的业务逻辑。
//
// 网关差异由 internal/pkg/payment.Provider 吸收；
// 本包只负责订单校验、金额/状态幂等与状态迁移，Mock/WeChat 两种模式走同一套代码。
package payment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	paymentpkg "bike-enroll/server/internal/pkg/payment"
	paymentrepo "bike-enroll/server/internal/repository/payment"
	"bike-enroll/server/internal/requestid"
)

// 预下单/回调相关错误，供 handler 映射为 openapi.yaml 错误码。
var (
	ErrInvalidArgument     = errors.New("invalid argument")
	ErrOrderNotFound       = errors.New("order not found")
	ErrOrderStatusInvalid  = errors.New("order status invalid")
	ErrInvalidNotification = errors.New("invalid payment notification")
	ErrRefundNotConfigured = errors.New("refund repository not configured")
	ErrRefundNotFound      = errors.New("refund not found")
	ErrRefundConflict      = errors.New("refund conflict")

	// ErrPaySignInvalid 保留给旧调用方；验签失败现在由 payment.Provider 返回，
	// 与 payment.ErrInvalidSignature 是同一个错误值。
	ErrPaySignInvalid = paymentpkg.ErrInvalidSignature
)

// EnrollmentNotifier 是支付成功后异步通知的投递接口。
type EnrollmentNotifier interface {
	EnqueueEnrollmentSuccess(orderID, requestID string)
}

// RefundInfo 是构造退款回调/响应所需的退款记录信息。
type RefundInfo struct {
	OutTradeNo   string
	OutRefundNo  string
	RefundID     string
	Amount       int
	RefundStatus string
}

// Service 聚合支付回调落库所需依赖。
type Service struct {
	repo       paymentrepo.Repository
	refundRepo paymentrepo.RefundRepository
	provider   paymentpkg.Provider
	notifier   EnrollmentNotifier
	logger     *slog.Logger
}

// New 创建支付服务。
func New(repo paymentrepo.Repository, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: repo, logger: logger}
}

// SetNotifier 注入支付成功通知投递器；必须在回调事务之外调用。
func (s *Service) SetNotifier(notifier EnrollmentNotifier) {
	s.notifier = notifier
}

// SetRefundRepository 注入退款回调落库仓储。
func (s *Service) SetRefundRepository(repo paymentrepo.RefundRepository) {
	s.refundRepo = repo
}

// SetProvider 注入支付 Provider，用于退款申请等主动调用。
func (s *Service) SetProvider(provider paymentpkg.Provider) {
	s.provider = provider
}

// GetOrderAmount 返回订单金额（单位分）。
//
// 仅供本地 mock 支付触发入口构造回调体使用；真实支付链路不调用。
func (s *Service) GetOrderAmount(ctx context.Context, orderID string) (int, error) {
	if orderID == "" {
		return 0, ErrInvalidArgument
	}
	order, _, err := s.repo.GetOrderWithUser(ctx, orderID)
	if err != nil {
		if errors.Is(err, paymentrepo.ErrOrderNotFound) {
			return 0, ErrOrderNotFound
		}
		return 0, fmt.Errorf("get order amount: %w", err)
	}
	return order.Amount, nil
}

// PreparePrepay 校验订单归属与状态，并组装与网关无关的预下单参数。
//
// 真正的网关调用由 handler 通过 Provider 完成，本方法不访问支付网关。
func (s *Service) PreparePrepay(ctx context.Context, userID, orderID string) (*paymentpkg.PrepayRequest, error) {
	if userID == "" || orderID == "" {
		return nil, ErrInvalidArgument
	}

	order, user, err := s.repo.GetOrderWithUser(ctx, orderID)
	if err != nil {
		if errors.Is(err, paymentrepo.ErrOrderNotFound) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("get order for prepay: %w", err)
	}
	if order.UserID != userID {
		return nil, ErrOrderNotFound
	}
	if order.Status != "pending_payment" {
		return nil, ErrOrderStatusInvalid
	}

	return &paymentpkg.PrepayRequest{
		OutTradeNo:  order.ID,
		Description: "赛事报名-" + order.ID,
		AmountTotal: order.Amount,
		OpenID:      user.OpenID,
	}, nil
}

// HandleNotify 处理已通过 Provider 验签/解析的支付回调。
//
// 状态迁移与 Mock/WeChat 模式无关：先锁 Order 行，再判断状态与金额，
// 最后在同一事务内迁移 Order + Enrollment；本方法不操作 Redis。
func (s *Service) HandleNotify(ctx context.Context, result *paymentpkg.NotifyResult) error {
	if result == nil {
		return ErrInvalidNotification
	}
	if result.EventType != "TRANSACTION.SUCCESS" {
		s.logger.InfoContext(ctx, "ignore non-success payment notify",
			"request_id", requestid.FromContext(ctx),
			"event_type", result.EventType,
		)
		return nil
	}
	if result.TradeState != "SUCCESS" {
		s.logger.InfoContext(ctx, "ignore non-success payment transaction",
			"request_id", requestid.FromContext(ctx),
			"trade_state", result.TradeState,
		)
		return nil
	}
	if result.OutTradeNo == "" || result.TransactionID == "" {
		return ErrInvalidNotification
	}

	outcome, err := s.repo.ApplyNotify(ctx, paymentrepo.NotifyParams{
		OutTradeNo:    result.OutTradeNo,
		TransactionID: result.TransactionID,
		AmountTotal:   result.Amount.Total,
		Now:           time.Now(),
	})
	if err != nil {
		return fmt.Errorf("apply payment notify: %w", err)
	}

	switch outcome {
	case paymentrepo.NotifyApplied:
		s.logger.InfoContext(ctx, "payment notify applied",
			"request_id", requestid.FromContext(ctx),
			"out_trade_no", result.OutTradeNo,
		)
	case paymentrepo.NotifyDuplicate:
		s.logger.InfoContext(ctx, "duplicate payment notify ignored",
			"request_id", requestid.FromContext(ctx),
			"out_trade_no", result.OutTradeNo,
		)
	case paymentrepo.NotifyAmountMismatch:
		s.logger.WarnContext(ctx, "PAY_AMOUNT_MISMATCH",
			"request_id", requestid.FromContext(ctx),
			"out_trade_no", result.OutTradeNo,
			"paid_amount", result.Amount.Total,
		)
	case paymentrepo.NotifyUnknownOrder:
		s.logger.WarnContext(ctx, "unknown out_trade_no in payment notify",
			"request_id", requestid.FromContext(ctx),
			"out_trade_no", result.OutTradeNo,
		)
	}

	// ApplyNotify 已提交事务；通知只在此之后异步投递，绝不在支付事务内发送。
	if outcome == paymentrepo.NotifyApplied && s.notifier != nil {
		s.notifier.EnqueueEnrollmentSuccess(result.OutTradeNo, requestid.FromContext(ctx))
	}
	return nil
}

// ApplyRefund 申请退款：out_refund_no 幂等 → 调 Provider.Refund → 落退款记录并把 Order 置为 refunding。
//
// 本方法不操作 Redis；名额回补只发生在退款成功回调。
func (s *Service) ApplyRefund(ctx context.Context, orderID, outRefundNo string) (*paymentpkg.RefundResult, error) {
	if orderID == "" || outRefundNo == "" {
		return nil, ErrInvalidArgument
	}
	if s.provider == nil || s.refundRepo == nil {
		return nil, ErrRefundNotConfigured
	}

	existing, err := s.refundRepo.GetByOutRefundNo(ctx, outRefundNo)
	switch {
	case err == nil:
		if existing.OrderID != orderID {
			return nil, ErrRefundConflict
		}
		return &paymentpkg.RefundResult{
			Status:   existing.Status,
			RefundID: derefRefundID(existing.RefundID),
		}, nil
	case errors.Is(err, paymentrepo.ErrRefundNotFound):
		// 继续申请
	default:
		return nil, fmt.Errorf("get refund by out_refund_no: %w", err)
	}

	order, err := s.repo.GetOrder(ctx, orderID)
	if err != nil {
		if errors.Is(err, paymentrepo.ErrOrderNotFound) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("get order for refund: %w", err)
	}
	if order.Status != "paid" {
		return nil, ErrOrderStatusInvalid
	}

	result, err := s.provider.Refund(ctx, paymentpkg.RefundRequest{
		OutTradeNo:  orderID,
		OutRefundNo: outRefundNo,
		Amount:      order.Amount,
	})
	if err != nil {
		return nil, fmt.Errorf("apply refund: %w", err)
	}

	refund, err := s.refundRepo.StartRefund(ctx, paymentrepo.StartRefundParams{
		OrderID:     orderID,
		OutRefundNo: outRefundNo,
		RefundID:    result.RefundID,
		Status:      result.Status,
		Amount:      order.Amount,
		Now:         time.Now(),
	})
	if err != nil {
		switch {
		case errors.Is(err, paymentrepo.ErrOrderNotFound):
			return nil, ErrOrderNotFound
		case errors.Is(err, paymentrepo.ErrRefundOrderStatus):
			return nil, ErrOrderStatusInvalid
		case errors.Is(err, paymentrepo.ErrRefundConflict):
			return nil, ErrRefundConflict
		default:
			return nil, fmt.Errorf("start refund: %w", err)
		}
	}

	return &paymentpkg.RefundResult{
		Status:   refund.Status,
		RefundID: derefRefundID(refund.RefundID),
	}, nil
}

// GetRefundInfo 返回订单最新一笔退款记录，供 mock 退款回调入口构造 body。
func (s *Service) GetRefundInfo(ctx context.Context, orderID string) (*RefundInfo, error) {
	if orderID == "" {
		return nil, ErrInvalidArgument
	}
	if s.refundRepo == nil {
		return nil, ErrRefundNotConfigured
	}
	refund, err := s.refundRepo.GetLatestByOrder(ctx, orderID)
	if err != nil {
		if errors.Is(err, paymentrepo.ErrRefundNotFound) {
			return nil, ErrRefundNotFound
		}
		return nil, fmt.Errorf("get latest refund: %w", err)
	}
	return &RefundInfo{
		OutTradeNo:   orderID,
		OutRefundNo:  refund.OutRefundNo,
		RefundID:     derefRefundID(refund.RefundID),
		Amount:       refund.Amount,
		RefundStatus: refund.Status,
	}, nil
}

func derefRefundID(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// HandleRefundNotify 处理已通过 Provider 验签/解析的退款回调。
//
// 状态迁移全部在 RefundRepository 的事务内完成：
// 锁 Order → 幂等判断 → paid/refunding 判断 → SUCCESS 时迁移并回补 Redis；
// CLOSED/ABNORMAL 将 refunding 置为 refund_failed，Enrollment 不变、不回补 Redis。
// Mock/WeChat 走同一套逻辑。
func (s *Service) HandleRefundNotify(ctx context.Context, result *paymentpkg.RefundNotifyResult) error {
	if result == nil || result.OutTradeNo == "" || result.OutRefundNo == "" {
		return ErrInvalidNotification
	}
	if s.refundRepo == nil {
		return ErrRefundNotConfigured
	}

	status := strings.ToUpper(strings.TrimSpace(result.RefundStatus))
	switch status {
	case paymentpkg.RefundStatusSuccess, paymentpkg.RefundStatusClosed, paymentpkg.RefundStatusAbnormal:
	default:
		return fmt.Errorf("%w: unknown refund_status %q", ErrInvalidNotification, result.RefundStatus)
	}

	outcome, err := s.refundRepo.ApplyRefundNotify(ctx, paymentrepo.RefundParams{
		OutTradeNo:   result.OutTradeNo,
		OutRefundNo:  result.OutRefundNo,
		RefundID:     result.RefundID,
		RefundStatus: status,
		RefundAmount: result.Amount,
		Now:          time.Now(),
	})
	if err != nil {
		return fmt.Errorf("apply refund notify: %w", err)
	}

	switch outcome {
	case paymentrepo.RefundApplied:
		s.logger.InfoContext(ctx, "refund notify applied",
			"request_id", requestid.FromContext(ctx),
			"out_trade_no", result.OutTradeNo,
			"out_refund_no", result.OutRefundNo,
			"refund_id", result.RefundID,
			"refund_status", status,
			"refund_amount", result.Amount,
		)
	case paymentrepo.RefundAlreadyDone:
		s.logger.InfoContext(ctx, "duplicate refund notify ignored",
			"request_id", requestid.FromContext(ctx),
			"out_trade_no", result.OutTradeNo,
			"out_refund_no", result.OutRefundNo,
		)
	case paymentrepo.RefundNotSuccess:
		s.logger.WarnContext(ctx, "refund notify not success",
			"request_id", requestid.FromContext(ctx),
			"out_trade_no", result.OutTradeNo,
			"out_refund_no", result.OutRefundNo,
			"refund_id", result.RefundID,
			"refund_status", status,
		)
	case paymentrepo.RefundIgnoredOrderStatus:
		s.logger.WarnContext(ctx, "refund notify ignored for order status",
			"request_id", requestid.FromContext(ctx),
			"out_trade_no", result.OutTradeNo,
			"refund_status", status,
		)
	case paymentrepo.RefundUnknownOrder:
		s.logger.WarnContext(ctx, "unknown out_trade_no in refund notify",
			"request_id", requestid.FromContext(ctx),
			"alert", true,
			"out_trade_no", result.OutTradeNo,
			"out_refund_no", result.OutRefundNo,
		)
	}
	return nil
}
