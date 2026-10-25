package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"bike-enroll/server/internal/requestid"
)

// AccessLog 记录结构化访问日志。
//
// 只记录路径，不记录查询串与请求体，避免手机号、身份证号等敏感信息落日志
// （docs/constraints.md 第 17 条）。
func AccessLog(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}

		logger.InfoContext(c.Request.Context(), "http_request",
			"request_id", requestid.Get(c),
			"method", c.Request.Method,
			"route", route,
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
			"client_ip", c.ClientIP(),
		)
	}
}
