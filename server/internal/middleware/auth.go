package middleware

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	jwtx "bike-enroll/server/internal/pkg/jwt"
	"bike-enroll/server/internal/response"
)

// 认证信息在 gin.Context 中的键名。
const (
	ContextUserID = "auth.user_id"
	ContextRole   = "auth.role"
)

// TokenVerifier 解析并校验 JWT。
type TokenVerifier interface {
	Parse(raw string) (*jwtx.Claims, error)
}

// RequireAuth 校验 Bearer JWT，并写入 auth.user_id / auth.role。
func RequireAuth(verifier TokenVerifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
		if !ok || strings.TrimSpace(token) == "" {
			response.Error(c, http.StatusUnauthorized, response.CodeAuthInvalidToken, "缺少或非法的 Bearer token")
			return
		}
		if verifier == nil {
			response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "鉴权未配置")
			return
		}

		claims, err := verifier.Parse(token)
		if err != nil {
			if errors.Is(err, jwtx.ErrExpired) {
				response.Error(c, http.StatusUnauthorized, response.CodeAuthExpired, "登录已过期")
				return
			}
			response.Error(c, http.StatusUnauthorized, response.CodeAuthInvalidToken, "无效的 Bearer token")
			return
		}
		if claims.Subject == "" {
			response.Error(c, http.StatusUnauthorized, response.CodeAuthInvalidToken, "无效的 Bearer token")
			return
		}

		c.Set(ContextUserID, claims.Subject)
		c.Set(ContextRole, claims.Role)
		c.Next()
	}
}

// RequireAdmin 校验 role=admin（docs/constraints.md 第 21 条）。
//
// 依赖 RequireAuth 解析出的 ContextRole。
func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if role, _ := c.Get(ContextRole); role != "admin" {
			response.Error(c, http.StatusForbidden, response.CodeAuthForbidden, "需要管理员权限")
			return
		}
		c.Next()
	}
}
