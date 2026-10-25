// Package notification 处理 notifications tag 下的接口（订阅消息）。
package notification

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"bike-enroll/server/internal/middleware"
	"bike-enroll/server/internal/requestid"
	"bike-enroll/server/internal/response"
	notificationsvc "bike-enroll/server/internal/service/notification"
)

// SubscribeService 是 handler 依赖的订阅授权服务。
type SubscribeService interface {
	Subscribe(ctx context.Context, userID string, templateIDs []string) error
}

// Handler 聚合 notifications tag 下的 HTTP 处理器。
type Handler struct {
	subscribeSvc SubscribeService
}

// New 创建 notification Handler。subscribeSvc 为 nil 时保留 501 占位行为。
func New(subscribeSvc SubscribeService) *Handler {
	return &Handler{subscribeSvc: subscribeSvc}
}

// Subscribe 处理 POST /api/v1/notifications/subscribe（需 JWT）。
func (h *Handler) Subscribe(c *gin.Context) {
	if h.subscribeSvc == nil {
		response.NotImplemented(c)
		return
	}

	userID, ok := c.Get(middleware.ContextUserID)
	if !ok || userID == "" {
		response.Error(c, http.StatusUnauthorized, response.CodeAuthInvalidToken, "缺少有效的登录身份")
		return
	}

	var req SubscribeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		slog.WarnContext(c.Request.Context(), "subscribe notifications invalid request",
			"request_id", requestid.Get(c),
			"reason", "invalid_argument",
		)
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "请求参数校验失败")
		return
	}

	if err := h.subscribeSvc.Subscribe(c.Request.Context(), fmt.Sprint(userID), req.TemplateIDs); err != nil {
		if errors.Is(err, notificationsvc.ErrInvalidArgument) {
			slog.WarnContext(c.Request.Context(), "subscribe notifications invalid template ids",
				"request_id", requestid.Get(c),
				"reason", "invalid_argument",
			)
			response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "请求参数校验失败")
			return
		}
		slog.ErrorContext(c.Request.Context(), "subscribe notifications failed",
			"request_id", requestid.Get(c),
			"error_type", fmt.Sprintf("%T", err),
		)
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "服务器内部错误")
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// SubscribeRequest 对应 openapi.yaml 中 /notifications/subscribe 的请求体。
type SubscribeRequest struct {
	TemplateIDs []string `json:"template_ids" binding:"required"`
}
