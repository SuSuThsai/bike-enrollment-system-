// Package event 处理 events tag 下的接口（赛事浏览，公开）。
package event

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"bike-enroll/server/internal/model"
	"bike-enroll/server/internal/requestid"
	"bike-enroll/server/internal/response"
	eventsvc "bike-enroll/server/internal/service/event"
)

// EventService 是 handler 依赖的赛事查询服务。
type EventService interface {
	List(ctx context.Context, status string, page, pageSize int) (*eventsvc.ListResult, error)
	Detail(ctx context.Context, eventID string) (*eventsvc.DetailResult, error)
}

// Handler 聚合 events tag 下的 HTTP 处理器。
type Handler struct {
	svc EventService
}

// New 创建 event Handler。svc 为 nil 时保留 501 占位行为。
func New(svc EventService) *Handler {
	return &Handler{svc: svc}
}

// List 处理 GET /api/v1/events。
func (h *Handler) List(c *gin.Context) {
	if h.svc == nil {
		response.NotImplemented(c)
		return
	}

	var query ListQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		slog.WarnContext(c.Request.Context(), "list events invalid query",
			"request_id", requestid.Get(c),
			"reason", "invalid_argument",
		)
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "请求参数校验失败")
		return
	}

	page, pageSize := query.pageValues()
	result, err := h.svc.List(c.Request.Context(), query.RegistrationStatus, page, pageSize)
	if err != nil {
		if errors.Is(err, eventsvc.ErrInvalidArgument) {
			slog.WarnContext(c.Request.Context(), "list events invalid query",
				"request_id", requestid.Get(c),
				"reason", "invalid_argument",
			)
			response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "请求参数校验失败")
			return
		}
		slog.ErrorContext(c.Request.Context(), "list events failed",
			"request_id", requestid.Get(c),
			"error_type", fmt.Sprintf("%T", err),
		)
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "服务器内部错误")
		return
	}

	items := make([]EventListItem, 0, len(result.Events))
	for _, view := range result.Events {
		item := eventListItem(view)
		if price, ok := result.MinPrices[view.Event.ID]; ok {
			item.MinPrice = &price
		}
		items = append(items, item)
	}

	c.JSON(http.StatusOK, ListResponse{Items: items, Total: result.Total})
}

// Detail 处理 GET /api/v1/events/{event_id}。
func (h *Handler) Detail(c *gin.Context) {
	if h.svc == nil {
		response.NotImplemented(c)
		return
	}

	result, err := h.svc.Detail(c.Request.Context(), c.Param("event_id"))
	if err != nil {
		if errors.Is(err, eventsvc.ErrEventNotFound) {
			response.Error(c, http.StatusNotFound, response.CodeEventNotFound, "赛事不存在")
			return
		}
		slog.ErrorContext(c.Request.Context(), "get event detail failed",
			"request_id", requestid.Get(c),
			"error_type", fmt.Sprintf("%T", err),
		)
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "服务器内部错误")
		return
	}

	groups := make([]GroupResponse, 0, len(result.Groups))
	for _, view := range result.Groups {
		groups = append(groups, GroupResponse{
			ID:                view.Group.ID,
			EventID:           view.Group.EventID,
			Name:              view.Group.Name,
			Price:             view.Group.Price,
			TotalSlots:        view.Group.TotalSlots,
			RemainingSlots:    view.RemainingSlots,
			StartTime:         view.Group.StartTime,
			Status:            view.Status,
			FormSchema:        view.Group.FormSchema,
			DisclaimerVersion: view.Group.DisclaimerVersion,
		})
	}

	responseBody := EventDetailResponse{
		EventListItem: eventListItem(result.Event),
		Description:   result.Event.Event.Description,
		Rules:         result.Event.Event.Rules,
		Groups:        groups,
	}
	if price, ok := minPriceOf(groups); ok {
		responseBody.MinPrice = &price
	}

	c.JSON(http.StatusOK, responseBody)
}

// ListQuery 对应 GET /events 的查询参数。
type ListQuery struct {
	RegistrationStatus string `form:"registration_status"`
	Page               *int   `form:"page"`
	PageSize           *int   `form:"page_size"`
}

func (q ListQuery) pageValues() (int, int) {
	page := 1
	if q.Page != nil {
		page = *q.Page
	}
	size := 20
	if q.PageSize != nil {
		size = *q.PageSize
	}
	return page, size
}

// EventListItem 对应 openapi.yaml 的 EventListItem schema。
type EventListItem struct {
	ID                       string     `json:"id"`
	Title                    string     `json:"title"`
	CoverURL                 string     `json:"cover_url"`
	StartTime                time.Time  `json:"start_time"`
	EndTime                  time.Time  `json:"end_time"`
	DurationMinutes          int        `json:"duration_minutes"`
	Location                 string     `json:"location"`
	LifecycleStatus          string     `json:"lifecycle_status"`
	RegistrationStatus       string     `json:"registration_status"`
	RegistrationClosedReason *string    `json:"registration_closed_reason"`
	RegistrationOpenAt       *time.Time `json:"registration_open_at"`
	RegistrationDeadline     *time.Time `json:"registration_deadline"`
	MinPrice                 *int       `json:"min_price,omitempty"`
}

func eventListItem(view eventsvc.EventView) EventListItem {
	return EventListItem{
		ID:                       view.Event.ID,
		Title:                    view.Event.Title,
		CoverURL:                 view.Event.CoverURL,
		StartTime:                view.Event.StartTime,
		EndTime:                  view.Event.EndTime,
		DurationMinutes:          model.EventDurationMinutes(view.Event.StartTime, view.Event.EndTime),
		Location:                 view.Event.Location,
		LifecycleStatus:          view.Event.LifecycleStatus,
		RegistrationStatus:       view.RegistrationStatus.String(),
		RegistrationClosedReason: view.Event.RegistrationClosedReason,
		RegistrationOpenAt:       view.Event.RegistrationOpenAt,
		RegistrationDeadline:     view.Event.RegistrationDeadline,
	}
}

// ListResponse 是 GET /events 的响应体。
type ListResponse struct {
	Items []EventListItem `json:"items"`
	Total int64           `json:"total"`
}

// EventDetailResponse 对应 openapi.yaml 的 EventDetail schema。
type EventDetailResponse struct {
	EventListItem
	Description string          `json:"description"`
	Rules       string          `json:"rules,omitempty"`
	Groups      []GroupResponse `json:"groups"`
}

// GroupResponse 对应 openapi.yaml 的 Group schema。
type GroupResponse struct {
	ID                string         `json:"id"`
	EventID           string         `json:"event_id"`
	Name              string         `json:"name"`
	Price             int            `json:"price"`
	TotalSlots        int            `json:"total_slots"`
	RemainingSlots    int            `json:"remaining_slots"`
	StartTime         time.Time      `json:"start_time"`
	Status            string         `json:"status"`
	FormSchema        map[string]any `json:"form_schema,omitempty"`
	DisclaimerVersion string         `json:"disclaimer_version,omitempty"`
}

func minPriceOf(groups []GroupResponse) (int, bool) {
	if len(groups) == 0 {
		return 0, false
	}
	price := groups[0].Price
	for _, group := range groups[1:] {
		if group.Price < price {
			price = group.Price
		}
	}
	return price, true
}
