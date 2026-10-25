package enrollment_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	enrollmenthandler "bike-enroll/server/internal/handler/enrollment"
	"bike-enroll/server/internal/middleware"
	"bike-enroll/server/internal/model"
	enrollmentsvc "bike-enroll/server/internal/service/enrollment"
)

type fakeQueryService struct {
	items       []model.Enrollment
	item        *model.Enrollment
	listErr     error
	detailErr   error
	gotUserID   string
	gotEnrollID string
}

func (f *fakeQueryService) ListMine(_ context.Context, userID string) ([]model.Enrollment, error) {
	f.gotUserID = userID
	return f.items, f.listErr
}

func (f *fakeQueryService) Detail(_ context.Context, userID, enrollmentID string) (*model.Enrollment, error) {
	f.gotUserID = userID
	f.gotEnrollID = enrollmentID
	return f.item, f.detailErr
}

func newRouter(query *fakeQueryService, withUser bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	handler := enrollmenthandler.New(nil, nil, query)
	router := gin.New()
	if withUser {
		router.Use(func(c *gin.Context) {
			c.Set(middleware.ContextUserID, "user-1")
		})
	}
	router.GET("/my", handler.ListMine)
	router.GET("/:enrollment_id", handler.Detail)
	return router
}

func TestListMineMasksSensitiveFormData(t *testing.T) {
	now := time.Now()
	query := &fakeQueryService{items: []model.Enrollment{{
		ID:        "enrollment-1",
		UserID:    "user-1",
		EventID:   "event-1",
		GroupID:   "group-1",
		OrderID:   "order-1",
		Status:    "paid",
		FormData:  map[string]any{"name": "张三", "phone": "13800138000", "id_card": "330101199001011234"},
		CreatedAt: now,
	}}}
	router := newRouter(query, true)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/my", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d，期望 200: %s", rec.Code, rec.Body.String())
	}
	if query.gotUserID != "user-1" {
		t.Errorf("userID = %q，期望 user-1", query.gotUserID)
	}

	var body struct {
		Items []struct {
			ID       string         `json:"id"`
			Status   string         `json:"status"`
			FormData map[string]any `json:"form_data"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].ID != "enrollment-1" || body.Items[0].Status != "paid" {
		t.Fatalf("items 响应错误: %+v", body.Items)
	}
	form := body.Items[0].FormData
	if form["name"] != "张三" {
		t.Errorf("非敏感字段 name = %v，期望保留", form["name"])
	}
	if form["phone"] != "***" || form["id_card"] != "***" {
		t.Errorf("敏感字段未脱敏: %+v", form)
	}
}

func TestListMineEmptyReturnsEmptyArray(t *testing.T) {
	router := newRouter(&fakeQueryService{items: []model.Enrollment{}}, true)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/my", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d，期望 200: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != `{"items":[]}` {
		t.Errorf("空列表响应 = %s，期望 {\"items\":[]}", body)
	}
}

func TestListMineRequiresUser(t *testing.T) {
	router := newRouter(&fakeQueryService{}, false)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/my", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d，期望 401", rec.Code)
	}
}

func TestEnrollmentDetailReturnsFormData(t *testing.T) {
	query := &fakeQueryService{item: &model.Enrollment{
		ID:        "enrollment-1",
		UserID:    "user-1",
		EventID:   "event-1",
		GroupID:   "group-1",
		OrderID:   "order-1",
		Status:    "paid",
		FormData:  map[string]any{"name": "张三", "phone": "13800138000"},
		CreatedAt: time.Now(),
	}}
	router := newRouter(query, true)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/enrollment-1", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d，期望 200: %s", rec.Code, rec.Body.String())
	}
	if query.gotEnrollID != "enrollment-1" {
		t.Errorf("enrollmentID = %q", query.gotEnrollID)
	}

	var body struct {
		ID       string         `json:"id"`
		FormData map[string]any `json:"form_data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body.ID != "enrollment-1" || body.FormData["phone"] != "13800138000" {
		t.Errorf("详情响应错误: %+v", body)
	}
}

func TestEnrollmentDetailNotFound(t *testing.T) {
	query := &fakeQueryService{detailErr: enrollmentsvc.ErrEnrollmentNotFound}
	router := newRouter(query, true)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/missing", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d，期望 404", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body["code"] != "ENROLLMENT_NOT_FOUND" {
		t.Errorf("code = %q，期望 ENROLLMENT_NOT_FOUND", body["code"])
	}
}
