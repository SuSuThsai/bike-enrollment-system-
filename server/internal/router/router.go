// Package router 组装中间件与 /api/v1 路由分组。
//
// 分组与 docs/openapi.yaml 的 tags 一一对应：
// auth / events / enrollments / payments / notifications / admin。
package router

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"bike-enroll/server/internal/handler/admin"
	"bike-enroll/server/internal/handler/auth"
	"bike-enroll/server/internal/handler/enrollment"
	"bike-enroll/server/internal/handler/event"
	"bike-enroll/server/internal/handler/mockpay"
	"bike-enroll/server/internal/handler/notification"
	"bike-enroll/server/internal/handler/payment"
	"bike-enroll/server/internal/middleware"
	"bike-enroll/server/internal/requestid"
)

// New 构建 gin.Engine。
//
// 未注入的 handler 使用 501 占位实现，便于契约测试骨架独立运行；
// 生产入口通过 Option 注入已装配的 handler。
func New(logger *slog.Logger, opts ...Option) *gin.Engine {
	options := options{}
	for _, opt := range opts {
		opt(&options)
	}

	r := gin.New()
	r.Use(
		requestid.Middleware(),
		middleware.AccessLog(logger),
		middleware.Recovery(logger),
	)

	// 运维探活，不属于 OpenAPI 业务契约。
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	api := r.Group("/api/v1")
	registerAuth(api, options.authHandler)
	registerEvents(api, options.eventHandler)
	registerEnrollments(api, options.enrollmentHandler, options.tokenVerifier)
	registerPayments(api, options.paymentHandler, options.tokenVerifier)
	registerNotifications(api, options.notificationHandler, options.tokenVerifier)
	registerAdmin(api, options.adminHandler, options.tokenVerifier)

	// 仅 PAYMENT_MODE=mock 时存在的本地触发入口，不进入 OpenAPI 契约。
	registerMockPay(r, options.mockPayHandler, options.mockPayEnabled)

	return r
}

// Option 配置 router 的可注入 handler。
type Option func(*options)

type options struct {
	authHandler         *auth.Handler
	eventHandler        *event.Handler
	enrollmentHandler   *enrollment.Handler
	paymentHandler      *payment.Handler
	notificationHandler *notification.Handler
	adminHandler        *admin.Handler
	mockPayHandler      *mockpay.Handler
	mockPayEnabled      bool
	tokenVerifier       middleware.TokenVerifier
}

// WithAuth 注入已装配的 auth handler。
func WithAuth(handler *auth.Handler) Option {
	return func(options *options) {
		options.authHandler = handler
	}
}

// WithEvent 注入已装配的 event handler。
func WithEvent(handler *event.Handler) Option {
	return func(options *options) {
		options.eventHandler = handler
	}
}

// WithEnrollment 注入已装配的 enrollment handler。
func WithEnrollment(handler *enrollment.Handler) Option {
	return func(options *options) {
		options.enrollmentHandler = handler
	}
}

// WithPayment 注入已装配的 payment handler。
func WithPayment(handler *payment.Handler) Option {
	return func(options *options) {
		options.paymentHandler = handler
	}
}

// WithNotification 注入已装配的 notification handler。
func WithNotification(handler *notification.Handler) Option {
	return func(options *options) {
		options.notificationHandler = handler
	}
}

// WithAdmin 注入已装配的 admin handler。
func WithAdmin(handler *admin.Handler) Option {
	return func(options *options) {
		options.adminHandler = handler
	}
}

// WithMockPay 注入 mock 支付触发入口；enabled=false 时不注册任何 /internal/mock-pay 路由。
func WithMockPay(handler *mockpay.Handler, enabled bool) Option {
	return func(options *options) {
		options.mockPayHandler = handler
		options.mockPayEnabled = enabled
	}
}

// WithTokenVerifier 注入 JWT 校验器；未注入时受保护接口返回 500。
func WithTokenVerifier(verifier middleware.TokenVerifier) Option {
	return func(options *options) {
		options.tokenVerifier = verifier
	}
}

// registerAuth 注册 auth 分组（公开）。
func registerAuth(api *gin.RouterGroup, handler *auth.Handler) {
	if handler == nil {
		handler = auth.New(nil)
	}
	g := api.Group("/auth")
	g.POST("/wechat-login", handler.WechatLogin)
}

// registerEvents 注册 events 分组（公开）。
func registerEvents(api *gin.RouterGroup, handler *event.Handler) {
	if handler == nil {
		handler = event.New(nil)
	}
	g := api.Group("/events")
	g.GET("", handler.List)
	g.GET("/:event_id", handler.Detail)
}

// registerEnrollments 注册 enrollments 分组（需 JWT）。
func registerEnrollments(api *gin.RouterGroup, handler *enrollment.Handler, verifier middleware.TokenVerifier) {
	if handler == nil {
		handler = enrollment.New(nil, nil, nil)
	}
	g := api.Group("/enrollments", middleware.RequireAuth(verifier))
	g.POST("/lock", handler.Lock)
	g.POST("", handler.Submit)
	g.GET("/my", handler.ListMine)
	g.GET("/:enrollment_id", handler.Detail)
}

// registerPayments 注册 payments 分组。
// prepay 需 JWT；notify 是微信回调，由微信平台验签，不走 Bearer 鉴权。
func registerPayments(api *gin.RouterGroup, handler *payment.Handler, verifier middleware.TokenVerifier) {
	if handler == nil {
		handler = payment.New(nil, nil)
	}
	g := api.Group("/payments")
	g.POST("/prepay", middleware.RequireAuth(verifier), handler.Prepay)
	g.POST("/notify", handler.Notify)
}

// registerNotifications 注册 notifications 分组（需 JWT）。
func registerNotifications(api *gin.RouterGroup, handler *notification.Handler, verifier middleware.TokenVerifier) {
	if handler == nil {
		handler = notification.New(nil)
	}
	g := api.Group("/notifications", middleware.RequireAuth(verifier))
	g.POST("/subscribe", handler.Subscribe)
}

// registerAdmin 注册 admin 分组（需 JWT 且 role=admin）。
func registerAdmin(api *gin.RouterGroup, handler *admin.Handler, verifier middleware.TokenVerifier) {
	if handler == nil {
		handler = admin.New(nil)
	}
	g := api.Group("/admin", middleware.RequireAuth(verifier), middleware.RequireAdmin())
	g.POST("/events", handler.CreateEvent)
	g.PUT("/events/:event_id", handler.UpdateEvent)
	g.DELETE("/events/:event_id", handler.DeleteEvent)
	g.POST("/groups", handler.CreateGroup)
	g.PUT("/groups/:group_id", handler.UpdateGroup)
	g.DELETE("/groups/:group_id", handler.DeleteGroup)
	g.POST("/groups/:group_id/rebuild-remaining", handler.RebuildGroupRemaining)
	g.PATCH("/events/:event_id/registration/close", handler.CloseRegistration)
	g.PATCH("/events/:event_id/registration/open", handler.OpenRegistration)
	g.GET("/enrollments", handler.ListEnrollments)
	g.GET("/enrollments/export", handler.ExportEnrollments)
	g.GET("/stats/overview", handler.StatsOverview)
	g.GET("/events/:event_id/release-health", handler.ReleaseHealth)
}

// registerMockPay 仅在 PAYMENT_MODE=mock 时注册本地支付触发入口。
//
// 路径不在 /api/v1 下，避免与对外契约混淆；X-Mock-Secret 由 handler 校验。
func registerMockPay(r *gin.Engine, handler *mockpay.Handler, enabled bool) {
	if !enabled || handler == nil {
		return
	}
	g := r.Group("/internal/mock-pay")
	g.POST("/:order_id/success", handler.Success)
	g.POST("/:order_id/fail", handler.Fail)

	refund := r.Group("/internal/mock-refund")
	refund.POST("/:order_id/apply", handler.RefundApply)
	refund.POST("/:order_id/success", handler.RefundSuccess)
	refund.POST("/:order_id/closed", handler.RefundClosed)
}
