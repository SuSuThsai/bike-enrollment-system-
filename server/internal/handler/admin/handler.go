// Package admin 处理 admin tag 下的接口（管理后台）。
//
// 所有接口均需 Bearer JWT 且 role=admin（docs/constraints.md 第 21 条）。
package admin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"bike-enroll/server/internal/middleware"
	"bike-enroll/server/internal/model"
	adminrepo "bike-enroll/server/internal/repository/admin"
	"bike-enroll/server/internal/requestid"
	"bike-enroll/server/internal/response"
	adminsvc "bike-enroll/server/internal/service/admin"
)

const exportContentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

// Service 是 handler 依赖的管理后台服务。
type Service interface {
	CreateEvent(ctx context.Context, params adminrepo.CreateEventParams) (*adminrepo.EventDetailData, error)
	UpdateEvent(ctx context.Context, params adminrepo.UpdateEventParams) (*adminrepo.EventDetailData, error)
	EventDetail(ctx context.Context, eventID string) (*adminrepo.EventDetailData, error)
	DeleteEvent(ctx context.Context, adminID, eventID string) (*adminrepo.EventDeleteResult, error)
	CreateGroup(ctx context.Context, params adminrepo.CreateGroupParams) (*adminrepo.GroupUpdateResult, error)
	UpdateGroup(ctx context.Context, params adminrepo.UpdateGroupParams) (*adminrepo.GroupUpdateResult, error)
	DeleteGroup(ctx context.Context, adminID, groupID string) (*adminrepo.GroupDeleteResult, error)
	RebuildGroupRemaining(ctx context.Context, adminID, groupID string, force bool) (*adminrepo.RemainingRebuildResult, error)
	ListEnrollments(ctx context.Context, filter adminrepo.EnrollmentFilter) (*adminrepo.EnrollmentPage, error)
	Export(ctx context.Context, adminID string, filter adminrepo.ExportFilter) (*adminsvc.ExportResult, error)
	StatsOverview(ctx context.Context, eventID string) (*adminrepo.StatsOverviewData, error)
	ReleaseHealth(ctx context.Context, eventID string) (*adminrepo.ReleaseHealthData, error)
	CloseEventRegistration(ctx context.Context, adminID, eventID, reason string) (*adminsvc.RegistrationState, error)
	OpenEventRegistration(ctx context.Context, adminID, eventID string) (*adminsvc.RegistrationState, error)
}

// Handler 聚合 admin tag 下的 HTTP 处理器。
type Handler struct {
	svc Service
}

// New 创建 admin Handler。svc 为 nil 时保留 501 占位行为。
func New(svc Service) *Handler {
	return &Handler{svc: svc}
}

// CreateEvent 处理 POST /api/v1/admin/events。
func (h *Handler) CreateEvent(c *gin.Context) {
	if h.svc == nil {
		response.NotImplemented(c)
		return
	}
	var req CreateEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "请求参数校验失败")
		return
	}
	result, err := h.svc.CreateEvent(c.Request.Context(), adminrepo.CreateEventParams{
		Title:                req.Title,
		CoverURL:             req.CoverURL,
		RegistrationOpenAt:   req.RegistrationOpenAt,
		RegistrationDeadline: req.RegistrationDeadline,
		StartTime:            req.StartTime,
		EndTime:              req.EndTime,
		Location:             req.Location,
		Description:          req.Description,
		Rules:                req.Rules,
	})
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, eventDetailResponse(result))
}

// UpdateEvent 处理 PUT /api/v1/admin/events/:event_id。
func (h *Handler) UpdateEvent(c *gin.Context) {
	if h.svc == nil {
		response.NotImplemented(c)
		return
	}
	eventID := c.Param("event_id")
	if eventID == "" {
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "缺少 event_id")
		return
	}
	var req UpdateEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "请求参数校验失败")
		return
	}
	result, err := h.svc.UpdateEvent(c.Request.Context(), adminrepo.UpdateEventParams{
		EventID:              eventID,
		Title:                req.Title,
		CoverURL:             req.CoverURL,
		RegistrationOpenAt:   req.RegistrationOpenAt,
		RegistrationDeadline: req.RegistrationDeadline,
		StartTime:            req.StartTime,
		EndTime:              req.EndTime,
		Location:             req.Location,
		Description:          req.Description,
		Rules:                req.Rules,
	})
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, eventDetailResponse(result))
}

// CreateGroup 处理 POST /api/v1/admin/groups。
func (h *Handler) CreateGroup(c *gin.Context) {
	if h.svc == nil {
		response.NotImplemented(c)
		return
	}
	var req CreateGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "请求参数校验失败")
		return
	}
	if req.Price == nil {
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "price 必填")
		return
	}
	result, err := h.svc.CreateGroup(c.Request.Context(), adminrepo.CreateGroupParams{
		EventID:           req.EventID,
		Name:              req.Name,
		Price:             *req.Price,
		TotalSlots:        req.TotalSlots,
		StartTime:         req.StartTime,
		FormSchema:        req.FormSchema,
		DisclaimerVersion: req.DisclaimerVersion,
	})
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, groupResponse(*result.Group, result.RemainingSlots))
}

// UpdateGroup 处理 PUT /api/v1/admin/groups/:group_id。
func (h *Handler) UpdateGroup(c *gin.Context) {
	if h.svc == nil {
		response.NotImplemented(c)
		return
	}
	groupID := c.Param("group_id")
	if groupID == "" {
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "缺少 group_id")
		return
	}

	var req UpdateGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		slog.WarnContext(c.Request.Context(), "admin update group invalid request",
			"request_id", requestid.Get(c),
			"group_id", groupID,
		)
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "请求参数校验失败")
		return
	}
	if req.TotalSlots != nil && *req.TotalSlots < 1 {
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "total_slots 必须大于 0")
		return
	}
	if req.Price != nil && *req.Price < 0 {
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "price 不能为负")
		return
	}

	result, err := h.svc.UpdateGroup(c.Request.Context(), adminrepo.UpdateGroupParams{
		GroupID:           groupID,
		Name:              req.Name,
		Price:             req.Price,
		TotalSlots:        req.TotalSlots,
		StartTime:         req.StartTime,
		FormSchema:        req.FormSchema,
		DisclaimerVersion: req.DisclaimerVersion,
	})
	if err != nil {
		switch {
		case errors.Is(err, adminsvc.ErrInvalidArgument):
			response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "请求参数校验失败")
		case errors.Is(err, adminrepo.ErrGroupNotFound):
			response.Error(c, http.StatusNotFound, response.CodeGroupNotFound, "组别不存在")
		case errors.Is(err, adminrepo.ErrGroupSoldOut):
			response.Error(c, http.StatusConflict, response.CodeGroupSoldOut, "名额调整导致剩余为负")
		default:
			slog.ErrorContext(c.Request.Context(), "admin update group failed",
				"request_id", requestid.Get(c),
				"group_id", groupID,
				"error_type", fmt.Sprintf("%T", err),
			)
			response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "服务器内部错误")
		}
		return
	}

	status := result.Group.Status
	if status == "open" && result.RemainingSlots <= 0 {
		status = "sold_out"
	}
	c.JSON(http.StatusOK, GroupResponse{
		ID:                result.Group.ID,
		EventID:           result.Group.EventID,
		Name:              result.Group.Name,
		Price:             result.Group.Price,
		TotalSlots:        result.Group.TotalSlots,
		RemainingSlots:    result.RemainingSlots,
		StartTime:         result.Group.StartTime,
		Status:            status,
		FormSchema:        result.Group.FormSchema,
		DisclaimerVersion: result.Group.DisclaimerVersion,
	})
}

// DeleteGroup 处理 DELETE /api/v1/admin/groups/:group_id。
//
// 仅允许删除没有任何锁/报名/订单的组别（否则 409 GROUP_IN_USE）；
// 删除成功后同步清理 Redis group:{group_id}:remaining。
func (h *Handler) DeleteGroup(c *gin.Context) {
	if h.svc == nil {
		response.NotImplemented(c)
		return
	}
	groupID := c.Param("group_id")
	if groupID == "" {
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "缺少 group_id")
		return
	}
	adminID, ok := adminIDFrom(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeAuthInvalidToken, "缺少有效的登录身份")
		return
	}
	result, err := h.svc.DeleteGroup(c.Request.Context(), adminID, groupID)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, DeleteResponse{ID: result.GroupID, Deleted: true})
}

// DeleteEvent 处理 DELETE /api/v1/admin/events/:event_id。
//
// 赛事下仍有组别时返回 409 EVENT_HAS_GROUPS，必须先逐个删除空组别。
func (h *Handler) DeleteEvent(c *gin.Context) {
	if h.svc == nil {
		response.NotImplemented(c)
		return
	}
	eventID := c.Param("event_id")
	if eventID == "" {
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "缺少 event_id")
		return
	}
	adminID, ok := adminIDFrom(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeAuthInvalidToken, "缺少有效的登录身份")
		return
	}
	result, err := h.svc.DeleteEvent(c.Request.Context(), adminID, eventID)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, DeleteResponse{ID: result.EventID, Deleted: true})
}

// CloseRegistration 处理 PATCH /api/v1/admin/events/:event_id/registration/close。
//
// 幂等：已关闭时重复调用返回 200 与当前状态；仅更新报名开关字段，不动锁/报名/订单。
func (h *Handler) CloseRegistration(c *gin.Context) {
	if h.svc == nil {
		response.NotImplemented(c)
		return
	}
	eventID := strings.TrimSpace(c.Param("event_id"))
	if eventID == "" {
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "缺少 event_id")
		return
	}
	adminID, ok := adminIDFrom(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeAuthInvalidToken, "缺少有效的登录身份")
		return
	}

	var req CloseRegistrationRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "请求参数校验失败")
			return
		}
	}

	state, err := h.svc.CloseEventRegistration(c.Request.Context(), adminID, eventID, req.Reason)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, registrationStateResponse(state))
}

// OpenRegistration 处理 PATCH /api/v1/admin/events/:event_id/registration/open。
//
// 若截止时间已过，返回 409 REGISTRATION_DEADLINE_PASSED 且不修改数据库。
func (h *Handler) OpenRegistration(c *gin.Context) {
	if h.svc == nil {
		response.NotImplemented(c)
		return
	}
	eventID := strings.TrimSpace(c.Param("event_id"))
	if eventID == "" {
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "缺少 event_id")
		return
	}
	adminID, ok := adminIDFrom(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeAuthInvalidToken, "缺少有效的登录身份")
		return
	}

	state, err := h.svc.OpenEventRegistration(c.Request.Context(), adminID, eventID)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, registrationStateResponse(state))
}

// RebuildGroupRemaining 处理 POST /api/v1/admin/groups/:group_id/rebuild-remaining。
//
// 仅修复"Redis 计数 key 缺失"的组别：按对账等式反推 remaining 后写入。
// key 已存在且取值与等式不一致时返回 409 GROUP_COUNTER_EXISTS，
// 需显式带 force=true 才覆盖（覆盖前应停止该组别流量）。
func (h *Handler) RebuildGroupRemaining(c *gin.Context) {
	if h.svc == nil {
		response.NotImplemented(c)
		return
	}
	groupID := c.Param("group_id")
	if groupID == "" {
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "缺少 group_id")
		return
	}

	var query RebuildRemainingQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		slog.WarnContext(c.Request.Context(), "admin rebuild group remaining invalid query",
			"request_id", requestid.Get(c),
			"group_id", groupID,
		)
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "查询参数校验失败")
		return
	}

	adminID, ok := adminIDFrom(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeAuthInvalidToken, "缺少有效的登录身份")
		return
	}
	result, err := h.svc.RebuildGroupRemaining(c.Request.Context(), adminID, groupID, query.Force)
	if err != nil {
		h.writeError(c, err)
		return
	}

	if result.Mode == "conflict" {
		slog.WarnContext(c.Request.Context(), "admin rebuild group remaining conflict",
			"request_id", requestid.Get(c),
			"group_id", groupID,
			"previous_remaining", result.Previous,
			"expected_remaining", result.Expected,
		)
		response.Error(c, http.StatusConflict, response.CodeGroupCounterExists,
			fmt.Sprintf("名额计数已存在（当前 %d，按对账等式应为 %d）；确认覆盖请使用 force=true",
				result.Previous, result.Expected))
		return
	}

	body := RebuildRemainingResponse{
		GroupID:             result.GroupID,
		TotalSlots:          result.TotalSlots,
		RemainingSlots:      result.Expected,
		Mode:                result.Mode,
		ActiveLockCount:     result.ActiveLocks,
		PendingPaymentCount: result.PendingPayment,
		PaidCount:           result.PaidConfirmed,
	}
	if result.HasPrevious {
		previous := result.Previous
		body.PreviousRemaining = &previous
	}
	c.JSON(http.StatusOK, body)
}

// ListEnrollments 处理 GET /api/v1/admin/enrollments。
func (h *Handler) ListEnrollments(c *gin.Context) {
	if h.svc == nil {
		response.NotImplemented(c)
		return
	}
	var query ListEnrollmentsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "查询参数校验失败")
		return
	}
	page, pageSize := 1, 50
	if query.Page != nil {
		page = *query.Page
	}
	if query.PageSize != nil {
		pageSize = *query.PageSize
	}
	result, err := h.svc.ListEnrollments(c.Request.Context(), adminrepo.EnrollmentFilter{
		EventID:  strings.TrimSpace(query.EventID),
		GroupID:  strings.TrimSpace(query.GroupID),
		Status:   strings.TrimSpace(query.Status),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		h.writeError(c, err)
		return
	}
	items := make([]EnrollmentResponse, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, EnrollmentResponse{
			ID:        item.ID,
			EventID:   item.EventID,
			GroupID:   item.GroupID,
			UserID:    item.UserID,
			OrderID:   item.OrderID,
			Status:    item.Status,
			FormData:  item.FormData,
			CreatedAt: item.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, PaginatedEnrollmentsResponse{
		Items:    items,
		Total:    result.Total,
		Page:     page,
		PageSize: pageSize,
	})
}

// ExportEnrollments 处理 GET /api/v1/admin/enrollments/export。
func (h *Handler) ExportEnrollments(c *gin.Context) {
	if h.svc == nil {
		response.NotImplemented(c)
		return
	}
	eventID := strings.TrimSpace(c.Query("event_id"))
	if eventID == "" {
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "event_id 必填")
		return
	}
	adminID, ok := c.Get(middleware.ContextUserID)
	if !ok || adminID == "" {
		response.Error(c, http.StatusUnauthorized, response.CodeAuthInvalidToken, "缺少有效的登录身份")
		return
	}

	var fields []string
	if raw := strings.TrimSpace(c.Query("fields")); raw != "" {
		fields = strings.Split(raw, ",")
	}
	result, err := h.svc.Export(c.Request.Context(), fmt.Sprint(adminID), adminrepo.ExportFilter{
		EventID: eventID,
		GroupID: strings.TrimSpace(c.Query("group_id")),
		Fields:  fields,
	})
	if err != nil {
		switch {
		case errors.Is(err, adminsvc.ErrInvalidArgument):
			response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "导出参数非法")
		case errors.Is(err, adminsvc.ErrEventNotFound):
			response.Error(c, http.StatusNotFound, response.CodeEventNotFound, "赛事不存在")
		default:
			slog.ErrorContext(c.Request.Context(), "admin export enrollments failed",
				"request_id", requestid.Get(c),
				"event_id", eventID,
				"error_type", fmt.Sprintf("%T", err),
			)
			response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "服务器内部错误")
		}
		return
	}

	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="enrollments-%s.xlsx"`, eventID))
	c.Data(http.StatusOK, exportContentType, result.Data)
}

// StatsOverview 处理 GET /api/v1/admin/stats/overview。
func (h *Handler) StatsOverview(c *gin.Context) {
	if h.svc == nil {
		response.NotImplemented(c)
		return
	}
	eventID := strings.TrimSpace(c.Query("event_id"))
	if eventID == "" {
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "event_id 必填")
		return
	}
	result, err := h.svc.StatsOverview(c.Request.Context(), eventID)
	if err != nil {
		h.writeError(c, err)
		return
	}
	groups := make([]GroupStatsResponse, 0, len(result.Groups))
	for _, group := range result.Groups {
		groups = append(groups, GroupStatsResponse{
			GroupID:             group.GroupID,
			TotalSlots:          group.TotalSlots,
			ActiveLockCount:     group.ActiveLockCount,
			PendingPaymentCount: group.PendingPaymentCount,
			PaidCount:           group.PaidCount,
			RemainingSlots:      group.RemainingSlots,
		})
	}
	c.JSON(http.StatusOK, StatsOverviewResponse{EventID: result.EventID, Groups: groups})
}

// ReleaseHealth 处理 GET /api/v1/admin/events/:event_id/release-health。
//
// 只读诊断：锁过期超过 1 分钟仍为 active（锁泄漏）、订单 pending_payment 超过
// 15 分钟未释放（支付超时任务遗漏）。
func (h *Handler) ReleaseHealth(c *gin.Context) {
	if h.svc == nil {
		response.NotImplemented(c)
		return
	}
	eventID := strings.TrimSpace(c.Param("event_id"))
	if eventID == "" {
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "缺少 event_id")
		return
	}
	result, err := h.svc.ReleaseHealth(c.Request.Context(), eventID)
	if err != nil {
		h.writeError(c, err)
		return
	}

	groups := make([]GroupReleaseHealthResponse, 0, len(result.Groups))
	for _, group := range result.Groups {
		leaks := make([]LockLeakSampleResponse, 0, len(group.LeakSamples))
		for _, sample := range group.LeakSamples {
			leaks = append(leaks, LockLeakSampleResponse{
				Token:          sample.Token,
				ExpiresAt:      sample.ExpiresAt,
				OverdueSeconds: sample.OverdueSeconds,
			})
		}
		pending := make([]PendingTimeoutSampleResponse, 0, len(group.PendingSamples))
		for _, sample := range group.PendingSamples {
			pending = append(pending, PendingTimeoutSampleResponse{
				OrderID:        sample.OrderID,
				CreatedAt:      sample.CreatedAt,
				OverdueMinutes: sample.OverdueMinutes,
			})
		}
		groups = append(groups, GroupReleaseHealthResponse{
			GroupID:             group.GroupID,
			GroupName:           group.GroupName,
			LockLeakCount:       group.LockLeakCount,
			MaxOverdueSeconds:   group.MaxOverdueSeconds,
			PendingTimeoutCount: group.PendingTimeoutCount,
			LeakSamples:         leaks,
			PendingSamples:      pending,
		})
	}

	c.JSON(http.StatusOK, ReleaseHealthResponse{
		EventID:   result.EventID,
		CheckedAt: result.CheckedAt,
		Thresholds: ReleaseHealthThresholds{
			LockOverdueSeconds: result.LockOverdueSeconds,
			PayTimeoutMinutes:  result.PayTimeoutMinutes,
		},
		Groups: groups,
	})
}

func (h *Handler) writeError(c *gin.Context, err error) {
	var timeRangeErr *model.TimeRangeError
	switch {
	case errors.As(err, &timeRangeErr):
		response.Error(c, http.StatusBadRequest, response.CodeInvalidTimeRange, timeRangeErr.Message)
	case errors.Is(err, adminsvc.ErrInvalidArgument):
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "请求参数校验失败")
	case errors.Is(err, adminsvc.ErrEventNotFound), errors.Is(err, adminrepo.ErrEventNotFound):
		response.Error(c, http.StatusNotFound, response.CodeEventNotFound, "赛事不存在")
	case errors.Is(err, adminrepo.ErrGroupNotFound):
		response.Error(c, http.StatusNotFound, response.CodeGroupNotFound, "组别不存在")
	case errors.Is(err, adminrepo.ErrGroupSoldOut):
		response.Error(c, http.StatusConflict, response.CodeGroupSoldOut, "名额调整导致剩余为负")
	case errors.Is(err, adminrepo.ErrGroupInUse):
		response.Error(c, http.StatusConflict, response.CodeGroupInUse, "组别仍存在锁、报名或订单，禁止删除")
	case errors.Is(err, adminrepo.ErrEventHasGroups):
		response.Error(c, http.StatusConflict, response.CodeEventHasGroups, "赛事下仍有组别，请先删除组别")
	case errors.Is(err, adminrepo.ErrReconcileFailed):
		response.Error(c, http.StatusConflict, response.CodeGroupReconcileFailed,
			"按对账等式反推的剩余名额为负，拒绝写入 Redis")
	case errors.Is(err, adminrepo.ErrRegistrationDeadlinePassed):
		response.Error(c, http.StatusConflict, response.CodeRegistrationDeadlinePassed,
			"报名截止时间已过，无法重新开放")
	default:
		slog.ErrorContext(c.Request.Context(), "admin request failed",
			"request_id", requestid.Get(c),
			"path", c.FullPath(),
			"error_type", fmt.Sprintf("%T", err),
		)
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "服务器内部错误")
	}
}

// adminIDFrom 读取 JWT 中间件写入的管理员 ID（docs/constraints.md 第 21 条审计要求）。
func adminIDFrom(c *gin.Context) (string, bool) {
	value, ok := c.Get(middleware.ContextUserID)
	if !ok {
		return "", false
	}
	id, ok := value.(string)
	if !ok || strings.TrimSpace(id) == "" {
		return "", false
	}
	return id, true
}

// DeleteResponse 对应 DELETE /admin/events/{event_id} 与 /admin/groups/{group_id} 的响应体。
type DeleteResponse struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

// CloseRegistrationRequest 是 PATCH /admin/events/{event_id}/registration/close 的请求体。
type CloseRegistrationRequest struct {
	Reason string `json:"reason" binding:"omitempty,max=255"`
}

// RegistrationStateResponse 对应 openapi.yaml 的 EventRegistrationState。
type RegistrationStateResponse struct {
	EventID                    string     `json:"event_id"`
	LifecycleStatus            string     `json:"lifecycle_status"`
	RegistrationStatus         string     `json:"registration_status"`
	RegistrationManuallyClosed bool       `json:"registration_manually_closed"`
	RegistrationClosedReason   *string    `json:"registration_closed_reason"`
	RegistrationOpenAt         *time.Time `json:"registration_open_at"`
	RegistrationDeadline       *time.Time `json:"registration_deadline"`
}

func registrationStateResponse(state *adminsvc.RegistrationState) RegistrationStateResponse {
	return RegistrationStateResponse{
		EventID:                    state.Event.ID,
		LifecycleStatus:            state.Event.LifecycleStatus,
		RegistrationStatus:         state.Status.String(),
		RegistrationManuallyClosed: state.Event.RegistrationManuallyClosed,
		RegistrationClosedReason:   state.Event.RegistrationClosedReason,
		RegistrationOpenAt:         state.Event.RegistrationOpenAt,
		RegistrationDeadline:       state.Event.RegistrationDeadline,
	}
}

// RebuildRemainingQuery 是 POST /admin/groups/{group_id}/rebuild-remaining 的查询参数。
type RebuildRemainingQuery struct {
	Force bool `form:"force"`
}

// RebuildRemainingResponse 对应名额重建接口的响应体。
type RebuildRemainingResponse struct {
	GroupID             string `json:"group_id"`
	TotalSlots          int    `json:"total_slots"`
	RemainingSlots      int    `json:"remaining_slots"`
	PreviousRemaining   *int   `json:"previous_remaining,omitempty"`
	Mode                string `json:"mode"`
	ActiveLockCount     int64  `json:"active_lock_count"`
	PendingPaymentCount int64  `json:"pending_payment_count"`
	PaidCount           int64  `json:"paid_count"`
}

// UpdateGroupRequest 对应 openapi.yaml 中 /admin/groups/{group_id} 的请求体。
type UpdateGroupRequest struct {
	Name              *string         `json:"name"`
	Price             *int            `json:"price"`
	TotalSlots        *int            `json:"total_slots"`
	StartTime         *time.Time      `json:"start_time"`
	FormSchema        *map[string]any `json:"form_schema"`
	DisclaimerVersion *string         `json:"disclaimer_version"`
}

// CreateEventRequest 对应 POST /admin/events 的请求体。
// CreateEventRequest 对应 POST /admin/events 的请求体。
type CreateEventRequest struct {
	Title                string    `json:"title" binding:"required"`
	CoverURL             string    `json:"cover_url"`
	RegistrationOpenAt   time.Time `json:"registration_open_at" binding:"required"`
	RegistrationDeadline time.Time `json:"registration_deadline" binding:"required"`
	StartTime            time.Time `json:"start_time" binding:"required"`
	EndTime              time.Time `json:"end_time" binding:"required"`
	Location             string    `json:"location" binding:"required"`
	Description          string    `json:"description" binding:"required"`
	Rules                string    `json:"rules"`
}

// UpdateEventRequest 对应 PUT /admin/events/{event_id} 的请求体。
type UpdateEventRequest struct {
	Title                *string    `json:"title"`
	CoverURL             *string    `json:"cover_url"`
	RegistrationOpenAt   *time.Time `json:"registration_open_at"`
	RegistrationDeadline *time.Time `json:"registration_deadline"`
	StartTime            *time.Time `json:"start_time"`
	EndTime              *time.Time `json:"end_time"`
	Location             *string    `json:"location"`
	Description          *string    `json:"description"`
	Rules                *string    `json:"rules"`
}

// EventDetailResponse 对应 openapi.yaml 的 EventDetail。
type EventDetailResponse struct {
	ID                       string          `json:"id"`
	Title                    string          `json:"title"`
	CoverURL                 string          `json:"cover_url"`
	StartTime                time.Time       `json:"start_time"`
	EndTime                  time.Time       `json:"end_time"`
	DurationMinutes          int             `json:"duration_minutes"`
	Location                 string          `json:"location"`
	LifecycleStatus          string          `json:"lifecycle_status"`
	RegistrationStatus       string          `json:"registration_status"`
	RegistrationClosedReason *string         `json:"registration_closed_reason"`
	RegistrationOpenAt       *time.Time      `json:"registration_open_at"`
	RegistrationDeadline     *time.Time      `json:"registration_deadline"`
	MinPrice                 *int            `json:"min_price,omitempty"`
	Description              string          `json:"description"`
	Rules                    string          `json:"rules,omitempty"`
	Groups                   []GroupResponse `json:"groups"`
}

// GroupResponse 对应 openapi.yaml 的 Group。
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

// ListEnrollmentsQuery 对应 GET /admin/enrollments 的查询参数。
type ListEnrollmentsQuery struct {
	EventID  string `form:"event_id"`
	GroupID  string `form:"group_id"`
	Status   string `form:"status"`
	Page     *int   `form:"page"`
	PageSize *int   `form:"page_size"`
}

// EnrollmentResponse 对应 openapi.yaml 的 Enrollment。
type EnrollmentResponse struct {
	ID        string         `json:"id"`
	EventID   string         `json:"event_id"`
	GroupID   string         `json:"group_id"`
	UserID    string         `json:"user_id"`
	OrderID   string         `json:"order_id"`
	Status    string         `json:"status"`
	FormData  map[string]any `json:"form_data,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// PaginatedEnrollmentsResponse 对应 openapi.yaml 的 PaginatedEnrollments。
type PaginatedEnrollmentsResponse struct {
	Items    []EnrollmentResponse `json:"items"`
	Total    int64                `json:"total"`
	Page     int                  `json:"page"`
	PageSize int                  `json:"page_size"`
}

// GroupStatsResponse 是 StatsOverview 的单个组别统计。
type GroupStatsResponse struct {
	GroupID             string `json:"group_id"`
	TotalSlots          int    `json:"total_slots"`
	ActiveLockCount     int64  `json:"active_lock_count"`
	PendingPaymentCount int64  `json:"pending_payment_count"`
	PaidCount           int64  `json:"paid_count"`
	RemainingSlots      int    `json:"remaining_slots"`
}

// StatsOverviewResponse 对应 openapi.yaml 的 StatsOverview。
type StatsOverviewResponse struct {
	EventID string               `json:"event_id"`
	Groups  []GroupStatsResponse `json:"groups"`
}

// ReleaseHealthResponse 对应 openapi.yaml 的 ReleaseHealth。
type ReleaseHealthResponse struct {
	EventID    string                       `json:"event_id"`
	CheckedAt  time.Time                    `json:"checked_at"`
	Thresholds ReleaseHealthThresholds      `json:"thresholds"`
	Groups     []GroupReleaseHealthResponse `json:"groups"`
}

// ReleaseHealthThresholds 是诊断使用的阈值。
type ReleaseHealthThresholds struct {
	LockOverdueSeconds int `json:"lock_overdue_seconds"`
	PayTimeoutMinutes  int `json:"pay_timeout_minutes"`
}

// GroupReleaseHealthResponse 是单个组别的诊断结果。
type GroupReleaseHealthResponse struct {
	GroupID             string                         `json:"group_id"`
	GroupName           string                         `json:"group_name"`
	LockLeakCount       int64                          `json:"lock_leak_count"`
	MaxOverdueSeconds   int64                          `json:"max_overdue_seconds"`
	PendingTimeoutCount int64                          `json:"pending_timeout_count"`
	LeakSamples         []LockLeakSampleResponse       `json:"leak_samples"`
	PendingSamples      []PendingTimeoutSampleResponse `json:"pending_samples"`
}

// LockLeakSampleResponse 是锁泄漏样例。
type LockLeakSampleResponse struct {
	Token          string    `json:"token"`
	ExpiresAt      time.Time `json:"expires_at"`
	OverdueSeconds int64     `json:"overdue_seconds"`
}

// PendingTimeoutSampleResponse 是支付超时遗漏样例。
type PendingTimeoutSampleResponse struct {
	OrderID        string    `json:"order_id"`
	CreatedAt      time.Time `json:"created_at"`
	OverdueMinutes int64     `json:"overdue_minutes"`
}

func eventDetailResponse(data *adminrepo.EventDetailData) EventDetailResponse {
	groups := make([]GroupResponse, 0, len(data.Groups))
	for _, group := range data.Groups {
		groups = append(groups, groupResponse(group.Group, group.Remaining))
	}
	body := EventDetailResponse{
		ID:                       data.Event.ID,
		Title:                    data.Event.Title,
		CoverURL:                 data.Event.CoverURL,
		StartTime:                data.Event.StartTime,
		EndTime:                  data.Event.EndTime,
		DurationMinutes:          model.EventDurationMinutes(data.Event.StartTime, data.Event.EndTime),
		Location:                 data.Event.Location,
		LifecycleStatus:          data.Event.LifecycleStatus,
		RegistrationStatus:       model.ResolveRegistrationStatus(data.Event, time.Now()).String(),
		RegistrationClosedReason: data.Event.RegistrationClosedReason,
		RegistrationOpenAt:       data.Event.RegistrationOpenAt,
		RegistrationDeadline:     data.Event.RegistrationDeadline,
		Description:              data.Event.Description,
		Rules:                    data.Event.Rules,
		Groups:                   groups,
	}
	if price, ok := minGroupPrice(groups); ok {
		body.MinPrice = &price
	}
	return body
}

func groupResponse(group model.Group, remaining int) GroupResponse {
	status := group.Status
	if status == "open" && remaining <= 0 {
		status = "sold_out"
	}
	return GroupResponse{
		ID:                group.ID,
		EventID:           group.EventID,
		Name:              group.Name,
		Price:             group.Price,
		TotalSlots:        group.TotalSlots,
		RemainingSlots:    remaining,
		StartTime:         group.StartTime,
		Status:            status,
		FormSchema:        group.FormSchema,
		DisclaimerVersion: group.DisclaimerVersion,
	}
}

func minGroupPrice(groups []GroupResponse) (int, bool) {
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

// CreateGroupRequest 对应 openapi.yaml 中 /admin/groups 的请求体。
type CreateGroupRequest struct {
	EventID           string         `json:"event_id" binding:"required"`
	Name              string         `json:"name" binding:"required"`
	Price             *int           `json:"price" binding:"required"`
	TotalSlots        int            `json:"total_slots" binding:"min=1"`
	StartTime         time.Time      `json:"start_time" binding:"required"`
	FormSchema        map[string]any `json:"form_schema" binding:"required"`
	DisclaimerVersion string         `json:"disclaimer_version" binding:"required"`
}
