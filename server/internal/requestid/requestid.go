// Package requestid 为每个请求生成并透传 request_id。
//
// 约束（docs/constraints.md 第 18 条）：每个请求生成 request_id，
// 写入响应体与所有日志行。
package requestid

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Header 是 request_id 的请求/响应头名称。
const Header = "X-Request-ID"

const contextKey = "request_id"

type contextValueKey struct{}

// Middleware 复用上游传入的 request_id；缺失时生成 UUID v4。
func Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(Header)
		if id == "" {
			id = uuid.NewString()
		}
		c.Set(contextKey, id)
		c.Header(Header, id)
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), contextValueKey{}, id))
		c.Next()
	}
}

// Get 返回当前请求的 request_id，未设置时返回空字符串。
func Get(c *gin.Context) string {
	return c.GetString(contextKey)
}

// FromContext 返回 request context 中携带的 request_id。
func FromContext(ctx context.Context) string {
	id, _ := ctx.Value(contextValueKey{}).(string)
	return id
}
