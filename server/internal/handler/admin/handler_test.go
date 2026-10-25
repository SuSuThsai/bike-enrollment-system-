package admin_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	adminhandler "bike-enroll/server/internal/handler/admin"
	"bike-enroll/server/internal/middleware"
	"bike-enroll/server/internal/model"
	adminrepo "bike-enroll/server/internal/repository/admin"
	"bike-enroll/server/internal/requestid"
	adminsvc "bike-enroll/server/internal/service/admin"
)

const testRequestID = "req-admin-123"

// fakeService 实现 adminhandler.Service，可注入各分支的错误。
type fakeService struct {
	updateGroupErr error
	exportErr      error
	statsErr       error
	deleteEventErr error
	deleteGroupErr error
	rebuildErr     error
}

func (f *fakeService) CreateEvent(context.Context, adminrepo.CreateEventParams) (*adminrepo.EventDetailData, error) {
	return &adminrepo.EventDetailData{}, nil
}

func (f *fakeService) DeleteEvent(context.Context, string, string) (*adminrepo.EventDeleteResult, error) {
	if f.deleteEventErr != nil {
		return nil, f.deleteEventErr
	}
	return &adminrepo.EventDeleteResult{}, nil
}

func (f *fakeService) DeleteGroup(context.Context, string, string) (*adminrepo.GroupDeleteResult, error) {
	if f.deleteGroupErr != nil {
		return nil, f.deleteGroupErr
	}
	return &adminrepo.GroupDeleteResult{}, nil
}

func (f *fakeService) RebuildGroupRemaining(context.Context, string, string, bool) (*adminrepo.RemainingRebuildResult, error) {
	if f.rebuildErr != nil {
		return nil, f.rebuildErr
	}
	return &adminrepo.RemainingRebuildResult{Mode: "created"}, nil
}

func (f *fakeService) UpdateEvent(context.Context, adminrepo.UpdateEventParams) (*adminrepo.EventDetailData, error) {
	return &adminrepo.EventDetailData{}, nil
}

func (f *fakeService) EventDetail(context.Context, string) (*adminrepo.EventDetailData, error) {
	return &adminrepo.EventDetailData{}, nil
}

func (f *fakeService) CreateGroup(context.Context, adminrepo.CreateGroupParams) (*adminrepo.GroupUpdateResult, error) {
	return &adminrepo.GroupUpdateResult{}, nil
}

func (f *fakeService) UpdateGroup(context.Context, adminrepo.UpdateGroupParams) (*adminrepo.GroupUpdateResult, error) {
	if f.updateGroupErr != nil {
		return nil, f.updateGroupErr
	}
	return &adminrepo.GroupUpdateResult{}, nil
}

func (f *fakeService) ListEnrollments(context.Context, adminrepo.EnrollmentFilter) (*adminrepo.EnrollmentPage, error) {
	return &adminrepo.EnrollmentPage{}, nil
}

func (f *fakeService) Export(context.Context, string, adminrepo.ExportFilter) (*adminsvc.ExportResult, error) {
	if f.exportErr != nil {
		return nil, f.exportErr
	}
	return &adminsvc.ExportResult{}, nil
}

func (f *fakeService) StatsOverview(context.Context, string) (*adminrepo.StatsOverviewData, error) {
	if f.statsErr != nil {
		return nil, f.statsErr
	}
	return &adminrepo.StatsOverviewData{}, nil
}

func (f *fakeService) ReleaseHealth(context.Context, string) (*adminrepo.ReleaseHealthData, error) {
	if f.statsErr != nil {
		return nil, f.statsErr
	}
	return &adminrepo.ReleaseHealthData{}, nil
}

func (f *fakeService) CloseEventRegistration(context.Context, string, string, string) (*adminsvc.RegistrationState, error) {
	return &adminsvc.RegistrationState{Event: model.Event{ID: "event-1"}}, nil
}

func (f *fakeService) OpenEventRegistration(context.Context, string, string) (*adminsvc.RegistrationState, error) {
	return &adminsvc.RegistrationState{Event: model.Event{ID: "event-1"}}, nil
}

// captureLogs 把默认 slog 输出重定向到缓冲区，测试结束后恢复。
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buffer bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buffer
}

// newRouter 走真实的 requestid 中间件，模拟网关透传 X-Request-ID。
func newRouter(handler *adminhandler.Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(requestid.Middleware())
	router.Use(func(c *gin.Context) {
		c.Set(middleware.ContextUserID, "admin-1")
	})
	group := router.Group("/admin")
	group.PUT("/groups/:group_id", handler.UpdateGroup)
	group.DELETE("/groups/:group_id", handler.DeleteGroup)
	group.POST("/groups/:group_id/rebuild-remaining", handler.RebuildGroupRemaining)
	group.DELETE("/events/:event_id", handler.DeleteEvent)
	group.GET("/enrollments/export", handler.ExportEnrollments)
	group.GET("/stats/overview", handler.StatsOverview)
	return router
}

// TestAdminLogsCarryRequestID 覆盖 admin handler 中四处请求级日志：
// 日志行与错误响应体都必须带上 request_id（docs/constraints.md 第 18 条）。
func TestAdminLogsCarryRequestID(t *testing.T) {
	boom := errors.New("db unavailable")
	cases := []struct {
		name    string
		service *fakeService
		method  string
		target  string
		body    string
		wantMsg string
	}{
		{
			name:    "update group invalid body",
			service: &fakeService{},
			method:  http.MethodPut,
			target:  "/admin/groups/group-1",
			body:    `not-json`,
			wantMsg: "admin update group invalid request",
		},
		{
			name:    "update group failed",
			service: &fakeService{updateGroupErr: boom},
			method:  http.MethodPut,
			target:  "/admin/groups/group-1",
			body:    `{"total_slots":10}`,
			wantMsg: "admin update group failed",
		},
		{
			name:    "export failed",
			service: &fakeService{exportErr: boom},
			method:  http.MethodGet,
			target:  "/admin/enrollments/export?event_id=event-1",
			wantMsg: "admin export enrollments failed",
		},
		{
			name:    "rebuild remaining invalid query",
			service: &fakeService{},
			method:  http.MethodPost,
			target:  "/admin/groups/group-1/rebuild-remaining?force=not-bool",
			wantMsg: "admin rebuild group remaining invalid query",
		},
		{
			name:    "write error default",
			service: &fakeService{statsErr: boom},
			method:  http.MethodGet,
			target:  "/admin/stats/overview?event_id=event-1",
			wantMsg: "admin request failed",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			logs := captureLogs(t)
			router := newRouter(adminhandler.New(testCase.service))

			var body io.Reader
			if testCase.body != "" {
				body = strings.NewReader(testCase.body)
			}
			request := httptest.NewRequest(testCase.method, testCase.target, body)
			request.Header.Set(requestid.Header, testRequestID)
			if testCase.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			if recorder.Code < http.StatusBadRequest {
				t.Fatalf("status = %d，期望错误响应: %s", recorder.Code, recorder.Body.String())
			}

			logged := logs.String()
			if !strings.Contains(logged, testCase.wantMsg) {
				t.Fatalf("缺少日志 %q: %s", testCase.wantMsg, logged)
			}
			if !strings.Contains(logged, "request_id="+testRequestID) {
				t.Errorf("日志缺少 request_id: %s", logged)
			}

			responseBody := recorder.Body.String()
			if !strings.Contains(responseBody, `"request_id":"`+testRequestID+`"`) {
				t.Errorf("错误响应体缺少 request_id: %s", responseBody)
			}
		})
	}
}
