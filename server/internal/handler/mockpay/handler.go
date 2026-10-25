// Package mockpay 提供本地联调用的支付触发入口。
//
// 该 handler 只依赖 payment.Service，不直接访问 MySQL/Redis；
// success/fail 最终都调用与真实回调完全相同的 Service.HandleNotify。
package mockpay

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	paymentpkg "bike-enroll/server/internal/pkg/payment"
	"bike-enroll/server/internal/requestid"
	"bike-enroll/server/internal/response"
	paymentsvc "bike-enroll/server/internal/service/payment"
)

const (
	mockSecretHeader   = "X-Mock-Secret"
	codeRefundNotFound = "REFUND_NOT_FOUND"
	codeRefundConflict = "REFUND_CONFLICT"
)

// Service 是 mock 支付/退款触发入口依赖的业务能力。
type Service interface {
	GetOrderAmount(ctx context.Context, orderID string) (int, error)
	HandleNotify(ctx context.Context, result *paymentpkg.NotifyResult) error
	ApplyRefund(ctx context.Context, orderID, outRefundNo string) (*paymentpkg.RefundResult, error)
	GetRefundInfo(ctx context.Context, orderID string) (*paymentsvc.RefundInfo, error)
	HandleRefundNotify(ctx context.Context, result *paymentpkg.RefundNotifyResult) error
}

// Handler 处理 /internal/mock-pay/:order_id/{success,fail}。
type Handler struct {
	service Service
	secret  string
	logger  *slog.Logger
}

// New 创建 mock 支付触发 handler。
func New(service Service, secret string, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{service: service, secret: secret, logger: logger}
}

// Success 模拟一笔金额正确的支付成功回调。
func (h *Handler) Success(c *gin.Context) {
	h.handle(c, "success", 0)
}

// Fail 模拟一笔金额不符的支付回调，用于验证 PAY_AMOUNT_MISMATCH 分支。
func (h *Handler) Fail(c *gin.Context) {
	h.handle(c, "fail", 1)
}

// RefundApply 模拟申请退款：把 Order 置为 refunding，不操作 Redis。
func (h *Handler) RefundApply(c *gin.Context) {
	if !h.authorized(c) {
		h.logger.WarnContext(c.Request.Context(), "mock refund secret invalid",
			"request_id", requestid.Get(c),
			"path", c.FullPath(),
		)
		response.Error(c, http.StatusForbidden, response.CodeAuthForbidden, "mock refund secret invalid")
		return
	}

	orderID := strings.TrimSpace(c.Param("order_id"))
	if orderID == "" {
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "order_id 必填")
		return
	}

	var req mockRefundApplyRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "请求参数校验失败")
			return
		}
	}
	outRefundNo := strings.TrimSpace(req.OutRefundNo)
	if outRefundNo == "" {
		outRefundNo = "mock-refund-" + orderID
	}

	result, err := h.service.ApplyRefund(c.Request.Context(), orderID, outRefundNo)
	if err != nil {
		switch {
		case errors.Is(err, paymentsvc.ErrInvalidArgument):
			response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "请求参数校验失败")
		case errors.Is(err, paymentsvc.ErrOrderNotFound):
			response.Error(c, http.StatusNotFound, response.CodeOrderNotFound, "订单不存在")
		case errors.Is(err, paymentsvc.ErrOrderStatusInvalid):
			response.Error(c, http.StatusConflict, response.CodeOrderStatusInvalid, "订单状态不允许退款")
		case errors.Is(err, paymentsvc.ErrRefundConflict):
			response.Error(c, http.StatusConflict, codeRefundConflict, "退款单号冲突")
		default:
			h.logger.ErrorContext(c.Request.Context(), "mock refund apply failed",
				"request_id", requestid.Get(c),
				"order_id", orderID,
				"error_type", fmt.Sprintf("%T", err),
			)
			response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "服务器内部错误")
		}
		return
	}

	h.logger.InfoContext(c.Request.Context(), "mock refund applied",
		"request_id", requestid.Get(c),
		"order_id", orderID,
		"out_refund_no", outRefundNo,
		"refund_id", result.RefundID,
		"status", result.Status,
	)

	c.JSON(http.StatusOK, gin.H{
		"code":          "SUCCESS",
		"order_id":      orderID,
		"out_refund_no": outRefundNo,
		"refund_id":     result.RefundID,
		"status":        result.Status,
	})
}

// RefundSuccess 模拟退款成功回调。
func (h *Handler) RefundSuccess(c *gin.Context) {
	h.refundNotify(c, paymentpkg.RefundStatusSuccess)
}

// RefundClosed 模拟退款关闭回调（不迁移主状态、不回补 Redis）。
func (h *Handler) RefundClosed(c *gin.Context) {
	h.refundNotify(c, paymentpkg.RefundStatusClosed)
}

func (h *Handler) refundNotify(c *gin.Context, refundStatus string) {
	if !h.authorized(c) {
		h.logger.WarnContext(c.Request.Context(), "mock refund secret invalid",
			"request_id", requestid.Get(c),
			"path", c.FullPath(),
		)
		response.Error(c, http.StatusForbidden, response.CodeAuthForbidden, "mock refund secret invalid")
		return
	}

	orderID := strings.TrimSpace(c.Param("order_id"))
	if orderID == "" {
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "order_id 必填")
		return
	}

	info, err := h.service.GetRefundInfo(c.Request.Context(), orderID)
	if err != nil {
		switch {
		case errors.Is(err, paymentsvc.ErrInvalidArgument):
			response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "请求参数校验失败")
		case errors.Is(err, paymentsvc.ErrRefundNotFound):
			response.Error(c, http.StatusNotFound, codeRefundNotFound, "退款记录不存在")
		default:
			h.logger.ErrorContext(c.Request.Context(), "mock refund load failed",
				"request_id", requestid.Get(c),
				"order_id", orderID,
				"error_type", fmt.Sprintf("%T", err),
			)
			response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "服务器内部错误")
		}
		return
	}

	// 与微信退款回调 resource 解密后的结构一致。
	result := &paymentpkg.RefundNotifyResult{
		OutTradeNo:   info.OutTradeNo,
		OutRefundNo:  info.OutRefundNo,
		RefundID:     info.RefundID,
		RefundStatus: refundStatus,
		Amount:       info.Amount,
	}
	if err := h.service.HandleRefundNotify(c.Request.Context(), result); err != nil {
		switch {
		case errors.Is(err, paymentsvc.ErrInvalidNotification):
			response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "退款回调数据非法")
		default:
			h.logger.ErrorContext(c.Request.Context(), "mock refund notify failed",
				"request_id", requestid.Get(c),
				"order_id", orderID,
				"error_type", fmt.Sprintf("%T", err),
			)
			response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "服务器内部错误")
		}
		return
	}

	h.logger.InfoContext(c.Request.Context(), "mock refund notify triggered",
		"request_id", requestid.Get(c),
		"order_id", orderID,
		"out_refund_no", info.OutRefundNo,
		"refund_id", info.RefundID,
		"refund_status", refundStatus,
	)

	c.JSON(http.StatusOK, gin.H{
		"code":          "SUCCESS",
		"order_id":      orderID,
		"out_refund_no": info.OutRefundNo,
		"refund_status": refundStatus,
	})
}

func (h *Handler) handle(c *gin.Context, mode string, amountDelta int) {
	if !h.authorized(c) {
		h.logger.WarnContext(c.Request.Context(), "mock pay secret invalid",
			"request_id", requestid.Get(c),
			"path", c.FullPath(),
		)
		response.Error(c, http.StatusForbidden, response.CodeAuthForbidden, "mock pay secret invalid")
		return
	}

	orderID := c.Param("order_id")
	if strings.TrimSpace(orderID) == "" {
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "order_id 必填")
		return
	}

	amount, err := h.service.GetOrderAmount(c.Request.Context(), orderID)
	if err != nil {
		switch {
		case errors.Is(err, paymentsvc.ErrInvalidArgument):
			response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "order_id 非法")
		case errors.Is(err, paymentsvc.ErrOrderNotFound):
			response.Error(c, http.StatusNotFound, response.CodeOrderNotFound, "订单不存在")
		default:
			h.logger.ErrorContext(c.Request.Context(), "mock pay load order failed",
				"request_id", requestid.Get(c),
				"order_id", orderID,
				"error_type", errType(err),
			)
			response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "服务器内部错误")
		}
		return
	}

	// 与微信支付 V3 resource 解密后的结构一致；mock 触发点直接构造该结构。
	// success: amount = order.amount；fail: amount = order.amount + 1。
	result := &paymentpkg.NotifyResult{
		EventType:     "TRANSACTION.SUCCESS",
		OutTradeNo:    orderID,
		TransactionID: uuid.NewString(),
		TradeState:    "SUCCESS",
		Amount: paymentpkg.NotifyAmount{
			Total:    amount + amountDelta,
			Currency: "CNY",
		},
	}

	if err := h.service.HandleNotify(c.Request.Context(), result); err != nil {
		switch {
		case errors.Is(err, paymentsvc.ErrInvalidNotification):
			response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "支付回调数据非法")
		default:
			h.logger.ErrorContext(c.Request.Context(), "mock pay notify failed",
				"request_id", requestid.Get(c),
				"order_id", orderID,
				"error_type", errType(err),
			)
			response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "服务器内部错误")
		}
		return
	}

	h.logger.InfoContext(c.Request.Context(), "mock pay triggered",
		"request_id", requestid.Get(c),
		"order_id", orderID,
		"transaction_id", result.TransactionID,
		"amount", result.Amount.Total,
		"mode", mode,
	)

	c.JSON(http.StatusOK, gin.H{
		"code":           "SUCCESS",
		"mode":           mode,
		"order_id":       orderID,
		"transaction_id": result.TransactionID,
		"amount":         result.Amount.Total,
		"currency":       result.Amount.Currency,
	})
}

// authorized 使用常量时间比较，避免按字符逐位泄露 secret。
//
// MOCK_PAY_SECRET 为空时一律拒绝，避免空密钥把接口变成公开的“标记已支付”入口。
func (h *Handler) authorized(c *gin.Context) bool {
	expected := strings.TrimSpace(h.secret)
	if expected == "" {
		return false
	}
	provided := c.GetHeader(mockSecretHeader)
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

type mockRefundApplyRequest struct {
	OutRefundNo string `json:"out_refund_no"`
}

func errType(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%T", err)
}
