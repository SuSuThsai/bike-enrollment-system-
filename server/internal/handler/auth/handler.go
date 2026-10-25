// Package auth 处理 auth tag 下的接口（微信登录与令牌签发）。
package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"bike-enroll/server/internal/requestid"
	"bike-enroll/server/internal/response"
	authsvc "bike-enroll/server/internal/service/auth"
)

// LoginService 是 handler 依赖的登录服务。
type LoginService interface {
	Login(ctx context.Context, code string) (*authsvc.LoginResult, error)
}

// Handler 聚合 auth tag 下的 HTTP 处理器。
type Handler struct {
	svc LoginService
}

// New 创建 auth Handler。svc 为 nil 时保留 501 占位行为，供契约测试骨架使用。
func New(svc LoginService) *Handler {
	return &Handler{svc: svc}
}

// WechatLogin 处理 POST /api/v1/auth/wechat-login。
//
// 成功：200 {token, user}；参数非法：400 INVALID_ARGUMENT；
// code 无效：400 AUTH_INVALID_CODE；依赖异常：500 INTERNAL_ERROR。
func (h *Handler) WechatLogin(c *gin.Context) {
	if h.svc == nil {
		response.NotImplemented(c)
		return
	}

	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		slog.WarnContext(c.Request.Context(), "wechat login invalid request",
			"request_id", requestid.Get(c),
			"reason", "invalid_argument",
		)
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "请求参数校验失败")
		return
	}

	result, err := h.svc.Login(c.Request.Context(), req.Code)
	if err != nil {
		switch {
		case errors.Is(err, authsvc.ErrInvalidArgument):
			slog.WarnContext(c.Request.Context(), "wechat login rejected",
				"request_id", requestid.Get(c),
				"reason", "invalid_argument",
			)
			response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "code 不能为空")
		case errors.Is(err, authsvc.ErrInvalidCode):
			// 不记录 code / openid；只记录错误分类。
			slog.WarnContext(c.Request.Context(), "wechat login rejected",
				"request_id", requestid.Get(c),
				"reason", "invalid_code",
			)
			response.Error(c, http.StatusBadRequest, response.CodeAuthInvalidCode, "微信 code 无效或已过期")
		default:
			// 底层错误可能包含 openid 等敏感值，只记录错误类型，不记录原文。
			slog.ErrorContext(c.Request.Context(), "wechat login failed",
				"request_id", requestid.Get(c),
				"error_type", fmt.Sprintf("%T", err),
			)
			response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "登录失败")
		}
		return
	}

	c.JSON(http.StatusOK, LoginResponse{
		Token: result.Token,
		User: UserResponse{
			ID:        result.User.ID,
			Nickname:  result.User.Nickname,
			AvatarURL: result.User.AvatarURL,
			Role:      result.User.Role,
		},
	})
}

// LoginRequest 对应 openapi.yaml 中 /auth/wechat-login 的请求体。
type LoginRequest struct {
	Code string `json:"code" binding:"required"`
}

// LoginResponse 对应 openapi.yaml 中登录成功的响应体。
type LoginResponse struct {
	Token string       `json:"token"`
	User  UserResponse `json:"user"`
}

// UserResponse 对应 openapi.yaml 的 User schema；不包含 openid。
type UserResponse struct {
	ID        string `json:"id"`
	Nickname  string `json:"nickname"`
	AvatarURL string `json:"avatar_url"`
	Role      string `json:"role"`
}
