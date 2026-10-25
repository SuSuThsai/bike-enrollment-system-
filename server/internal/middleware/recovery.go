package middleware

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"

	"bike-enroll/server/internal/requestid"
	"bike-enroll/server/internal/response"
)

// Recovery 捕获 panic，记录堆栈并返回统一错误响应。
func Recovery(logger *slog.Logger) gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, recovered any) {
		logger.ErrorContext(c.Request.Context(), "panic_recovered",
			"request_id", requestid.Get(c),
			"panic", fmt.Sprint(recovered),
			"stack", string(debug.Stack()),
		)
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "服务器内部错误")
	})
}
