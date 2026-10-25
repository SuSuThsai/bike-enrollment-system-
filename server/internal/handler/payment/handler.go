// Package payment 处理 payments tag 下的接口（微信支付/本地 mock）。
//
// handler 只负责 HTTP 编排：校验订单 → 调用 Provider 预下单；
// 回调则先 Provider.VerifyNotify，再交给 Service.HandleNotify。
// handler 内不存在按 mock/wechat 分支的逻辑。
package payment

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"bike-enroll/server/internal/middleware"
	paymentpkg "bike-enroll/server/internal/pkg/payment"
	"bike-enroll/server/internal/requestid"
	"bike-enroll/server/internal/response"
	paymentsvc "bike-enroll/server/internal/service/payment"
)

// Service 是 handler 依赖的支付业务逻辑。
type Service interface {
	PreparePrepay(ctx context.Context, userID, orderID string) (*paymentpkg.PrepayRequest, error)
	HandleNotify(ctx context.Context, result *paymentpkg.NotifyResult) error
}

// Handler 聚合 payments tag 下的 HTTP 处理器。
type Handler struct {
	provider paymentpkg.Provider
	service  Service
}

// New 创建 payment Handler。依赖为 nil 时保留 501 占位行为。
func New(provider paymentpkg.Provider, service Service) *Handler {
	return &Handler{provider: provider, service: service}
}

// Prepay 处理 POST /api/v1/payments/prepay（需 JWT）。
func (h *Handler) Prepay(c *gin.Context) {
	if h.provider == nil || h.service == nil {
		response.NotImplemented(c)
		return
	}

	userID, ok := c.Get(middleware.ContextUserID)
	if !ok || userID == "" {
		logPrepayRejected(c, "", "missing_user")
		response.Error(c, http.StatusUnauthorized, response.CodeAuthInvalidToken, "缺少有效的登录身份")
		return
	}

	var req PrepayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		slog.WarnContext(c.Request.Context(), "prepay invalid request",
			"request_id", requestid.Get(c),
			"reason", "invalid_argument",
		)
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "请求参数校验失败")
		return
	}

	prepayRequest, err := h.service.PreparePrepay(c.Request.Context(), fmt.Sprint(userID), req.OrderID)
	if err != nil {
		switch {
		case errors.Is(err, paymentsvc.ErrInvalidArgument):
			logPrepayRejected(c, req.OrderID, "invalid_argument")
			response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "请求参数校验失败")
		case errors.Is(err, paymentsvc.ErrOrderNotFound):
			logPrepayRejected(c, req.OrderID, "order_not_found")
			response.Error(c, http.StatusNotFound, response.CodeOrderNotFound, "订单不存在")
		case errors.Is(err, paymentsvc.ErrOrderStatusInvalid):
			logPrepayRejected(c, req.OrderID, "order_status_invalid")
			response.Error(c, http.StatusConflict, response.CodeOrderStatusInvalid, "订单状态不允许支付")
		default:
			slog.ErrorContext(c.Request.Context(), "prepay prepare failed",
				"request_id", requestid.Get(c),
				"error_type", fmt.Sprintf("%T", err),
			)
			response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "服务器内部错误")
		}
		return
	}

	result, err := h.provider.Prepay(c.Request.Context(), *prepayRequest)
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "prepay failed",
			"request_id", requestid.Get(c),
			"error_type", fmt.Sprintf("%T", err),
		)
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "服务器内部错误")
		return
	}

	c.JSON(http.StatusOK, result)
}

func logPrepayRejected(c *gin.Context, orderID, reason string) {
	slog.WarnContext(c.Request.Context(), "prepay rejected",
		"request_id", requestid.Get(c),
		"order_id", orderID,
		"reason", reason,
	)
}

// Notify 处理 POST /api/v1/payments/notify（公开，由支付平台回调）。
func (h *Handler) Notify(c *gin.Context) {
	if h.provider == nil || h.service == nil {
		response.NotImplemented(c)
		return
	}

	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		slog.WarnContext(c.Request.Context(), "read payment notify body failed",
			"request_id", requestid.Get(c),
		)
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "回调请求体读取失败")
		return
	}

	result, err := h.provider.VerifyNotify(c.Request.Context(), c.Request.Header, body)
	if err != nil {
		switch {
		case errors.Is(err, paymentpkg.ErrInvalidSignature):
			slog.WarnContext(c.Request.Context(), "payment notify signature invalid",
				"request_id", requestid.Get(c),
			)
			response.Error(c, http.StatusUnauthorized, response.CodePaySignInvalid, "支付回调验签失败")
		case errors.Is(err, paymentpkg.ErrInvalidPayload):
			slog.WarnContext(c.Request.Context(), "payment notify payload invalid",
				"request_id", requestid.Get(c),
			)
			response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "支付回调数据非法")
		default:
			slog.ErrorContext(c.Request.Context(), "payment notify verify failed",
				"request_id", requestid.Get(c),
				"error_type", fmt.Sprintf("%T", err),
			)
			response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "服务器内部错误")
		}
		return
	}

	// 验签/解析成功后，必须进入统一的状态迁移逻辑；Mock 模式不得跳过任何一步。
	if err := h.service.HandleNotify(c.Request.Context(), result); err != nil {
		switch {
		case errors.Is(err, paymentsvc.ErrInvalidNotification):
			slog.WarnContext(c.Request.Context(), "payment notify payload invalid",
				"request_id", requestid.Get(c),
			)
			response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "支付回调数据非法")
		default:
			slog.ErrorContext(c.Request.Context(), "payment notify failed",
				"request_id", requestid.Get(c),
				"error_type", fmt.Sprintf("%T", err),
			)
			response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "服务器内部错误")
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": "SUCCESS"})
}

// PrepayRequest 对应 openapi.yaml 中 /payments/prepay 的请求体。
type PrepayRequest struct {
	OrderID string `json:"order_id" binding:"required"`
}
