package router_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	jwtlib "github.com/golang-jwt/jwt/v5"

	jwtx "bike-enroll/server/internal/pkg/jwt"
	"bike-enroll/server/internal/requestid"
	"bike-enroll/server/internal/response"
	"bike-enroll/server/internal/router"
)

func newTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	return router.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestRouteTable 锁定路由与 docs/openapi.yaml 的对应关系，防止后续改动漏挂接口。
func TestRouteTable(t *testing.T) {
	r := newTestRouter(t)

	got := make(map[string]bool, len(r.Routes()))
	for _, route := range r.Routes() {
		got[route.Method+" "+route.Path] = true
	}

	want := []string{
		"GET /healthz",

		"POST /api/v1/auth/wechat-login",

		"GET /api/v1/events",
		"GET /api/v1/events/:event_id",

		"POST /api/v1/enrollments/lock",
		"POST /api/v1/enrollments",
		"GET /api/v1/enrollments/my",
		"GET /api/v1/enrollments/:enrollment_id",

		"POST /api/v1/payments/prepay",
		"POST /api/v1/payments/notify",

		"POST /api/v1/notifications/subscribe",

		"POST /api/v1/admin/events",
		"PUT /api/v1/admin/events/:event_id",
		"DELETE /api/v1/admin/events/:event_id",
		"POST /api/v1/admin/groups",
		"PUT /api/v1/admin/groups/:group_id",
		"DELETE /api/v1/admin/groups/:group_id",
		"POST /api/v1/admin/groups/:group_id/rebuild-remaining",
		"GET /api/v1/admin/enrollments",
		"GET /api/v1/admin/enrollments/export",
		"GET /api/v1/admin/stats/overview",
		"GET /api/v1/admin/events/:event_id/release-health",
		"PATCH /api/v1/admin/events/:event_id/registration/close",
		"PATCH /api/v1/admin/events/:event_id/registration/open",
	}

	for _, route := range want {
		if !got[route] {
			t.Errorf("缺少路由 %s", route)
			continue
		}
		delete(got, route)
	}
	for route := range got {
		t.Errorf("存在 OpenAPI 未定义的路由 %s", route)
	}
}

// TestRequireAuthRejectsMissingToken 校验鉴权占位中间件 fail-closed 且错误体带 request_id。
func TestRequireAuthRejectsMissingToken(t *testing.T) {
	r := newTestRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/enrollments/lock", strings.NewReader(`{"group_id":"group-1"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("状态码 = %d，期望 %d", w.Code, http.StatusUnauthorized)
	}

	var body response.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if body.Code != response.CodeAuthInvalidToken {
		t.Errorf("code = %q，期望 %q", body.Code, response.CodeAuthInvalidToken)
	}
	if body.RequestID == "" {
		t.Error("响应体缺少 request_id")
	}
	if headerID := w.Header().Get(requestid.Header); headerID != body.RequestID {
		t.Errorf("响应头 request_id = %q，响应体 = %q", headerID, body.RequestID)
	}
}

// TestPublicHandlerIsStub 校验公开路由已挂载，但业务逻辑仍为占位实现。
func TestPublicHandlerIsStub(t *testing.T) {
	r := newTestRouter(t)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/events", nil))

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("状态码 = %d，期望 %d", w.Code, http.StatusNotImplemented)
	}

	var body response.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if body.Code != response.CodeNotImplemented {
		t.Errorf("code = %q，期望 %q", body.Code, response.CodeNotImplemented)
	}
}

type roleTokenVerifier struct{}

func (roleTokenVerifier) Parse(raw string) (*jwtx.Claims, error) {
	switch raw {
	case "rider-token":
		return &jwtx.Claims{
			Role:             "rider",
			RegisteredClaims: jwtlib.RegisteredClaims{Subject: "rider-1"},
		}, nil
	case "admin-token":
		return &jwtx.Claims{
			Role:             "admin",
			RegisteredClaims: jwtlib.RegisteredClaims{Subject: "admin-1"},
		}, nil
	default:
		return nil, jwtx.ErrInvalidToken
	}
}

// TestAdminRoutesRequireAdminRole 校验所有 admin 路由在进入 handler 前拒绝 rider。
func TestAdminRoutesRequireAdminRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := router.New(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		router.WithTokenVerifier(roleTokenVerifier{}),
	)

	routes := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/admin/events"},
		{http.MethodPut, "/api/v1/admin/events/event-1"},
		{http.MethodPost, "/api/v1/admin/groups"},
		{http.MethodPut, "/api/v1/admin/groups/group-1"},
		{http.MethodGet, "/api/v1/admin/enrollments"},
		{http.MethodGet, "/api/v1/admin/enrollments/export?event_id=event-1"},
		{http.MethodGet, "/api/v1/admin/stats/overview?event_id=event-1"},
	}

	for _, route := range routes {
		req := httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer rider-token")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s status = %d，期望 403", route.method, route.path, w.Code)
			continue
		}
		var body response.ErrorResponse
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Errorf("%s %s 解析响应失败: %v", route.method, route.path, err)
			continue
		}
		if body.Code != response.CodeAuthForbidden {
			t.Errorf("%s %s code = %q，期望 %q", route.method, route.path, body.Code, response.CodeAuthForbidden)
		}
	}
}
