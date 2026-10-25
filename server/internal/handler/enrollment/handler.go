// Package enrollment 处理 enrollments tag 下的接口（名额锁定与报名）。
package enrollment

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
	"bike-enroll/server/internal/requestid"
	"bike-enroll/server/internal/response"
	enrollmentsvc "bike-enroll/server/internal/service/enrollment"
)

// LockService 是 handler 依赖的名额锁定服务。
type LockService interface {
	Lock(ctx context.Context, userID, groupID string) (*enrollmentsvc.LockResult, error)
}

// SubmitService 是 handler 依赖的提交报名服务。
type SubmitService interface {
	Submit(ctx context.Context, userID string, input enrollmentsvc.SubmitInput) (*enrollmentsvc.SubmitResult, error)
}

// QueryService 是 handler 依赖的报名查询服务。
type QueryService interface {
	ListMine(ctx context.Context, userID string) ([]model.Enrollment, error)
	Detail(ctx context.Context, userID, enrollmentID string) (*model.Enrollment, error)
}

// Handler 聚合 enrollments tag 下的 HTTP 处理器。
type Handler struct {
	lockSvc   LockService
	submitSvc SubmitService
	querySvc  QueryService
}

// New 创建 enrollment Handler。对应服务为 nil 时保留 501 占位行为。
func New(lockSvc LockService, submitSvc SubmitService, querySvc QueryService) *Handler {
	return &Handler{lockSvc: lockSvc, submitSvc: submitSvc, querySvc: querySvc}
}

// Lock 处理 POST /api/v1/enrollments/lock。
func (h *Handler) Lock(c *gin.Context) {
	if h.lockSvc == nil {
		response.NotImplemented(c)
		return
	}

	userID, ok := c.Get(middleware.ContextUserID)
	if !ok || userID == "" {
		logLockRejected(c, "", "missing_user")
		response.Error(c, http.StatusUnauthorized, response.CodeAuthInvalidToken, "缺少有效的登录身份")
		return
	}

	var req LockRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		slog.WarnContext(c.Request.Context(), "lock enrollment invalid request",
			"request_id", requestid.Get(c),
			"reason", "invalid_argument",
		)
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "请求参数校验失败")
		return
	}

	result, err := h.lockSvc.Lock(c.Request.Context(), fmt.Sprint(userID), req.GroupID)
	if err != nil {
		switch {
		case errors.Is(err, enrollmentsvc.ErrInvalidArgument):
			logLockRejected(c, req.GroupID, "invalid_argument")
			response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "请求参数校验失败")
		case errors.Is(err, enrollmentsvc.ErrGroupNotFound):
			logLockRejected(c, req.GroupID, "group_not_found")
			response.Error(c, http.StatusNotFound, response.CodeGroupNotFound, "组别不存在")
		case errors.Is(err, enrollmentsvc.ErrEventNotOpen):
			logLockRejected(c, req.GroupID, "event_not_open")
			response.Error(c, http.StatusConflict, response.CodeEventNotOpen, eventNotOpenMessage(err))
		case errors.Is(err, enrollmentsvc.ErrGroupClosed):
			logLockRejected(c, req.GroupID, "group_closed")
			response.Error(c, http.StatusConflict, response.CodeGroupClosed, "组别已关闭")
		case errors.Is(err, enrollmentsvc.ErrGroupSoldOut):
			logLockRejected(c, req.GroupID, "group_sold_out")
			response.Error(c, http.StatusConflict, response.CodeGroupSoldOut, "名额不足")
		case errors.Is(err, enrollmentsvc.ErrRateLimited):
			logLockRejected(c, req.GroupID, "rate_limited")
			response.Error(c, http.StatusTooManyRequests, response.CodeRateLimited, "请勿重复锁定")
		default:
			slog.ErrorContext(c.Request.Context(), "lock enrollment failed",
				"request_id", requestid.Get(c),
				"error_type", fmt.Sprintf("%T", err),
			)
			response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "服务器内部错误")
		}
		return
	}

	c.JSON(http.StatusOK, LockResponse{
		LockToken:      result.Token,
		ExpiresAt:      result.ExpiresAt,
		RemainingSlots: result.RemainingSlots,
	})
}

func logLockRejected(c *gin.Context, groupID, reason string) {
	slog.WarnContext(c.Request.Context(), "lock enrollment rejected",
		"request_id", requestid.Get(c),
		"group_id", groupID,
		"reason", reason,
	)
}

// eventNotOpenMessage 从 EventNotOpenError 取按原因生成的消息，兜底默认文案。
func eventNotOpenMessage(err error) string {
	var notOpen *enrollmentsvc.EventNotOpenError
	if errors.As(err, &notOpen) && notOpen.Message != "" {
		return notOpen.Message
	}
	return "赛事未开放报名"
}

// Submit 处理 POST /api/v1/enrollments（需 JWT）。
func (h *Handler) Submit(c *gin.Context) {
	if h.submitSvc == nil {
		response.NotImplemented(c)
		return
	}

	userID, ok := c.Get(middleware.ContextUserID)
	if !ok || userID == "" {
		logSubmitRejected(c, "", "missing_user")
		response.Error(c, http.StatusUnauthorized, response.CodeAuthInvalidToken, "缺少有效的登录身份")
		return
	}

	var req SubmitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		slog.WarnContext(c.Request.Context(), "submit enrollment invalid request",
			"request_id", requestid.Get(c),
			"reason", "invalid_argument",
		)
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "请求参数校验失败")
		return
	}
	if req.FormData == nil {
		slog.WarnContext(c.Request.Context(), "submit enrollment invalid request",
			"request_id", requestid.Get(c),
			"reason", "invalid_form_data",
		)
		response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "请求参数校验失败")
		return
	}

	result, err := h.submitSvc.Submit(c.Request.Context(), fmt.Sprint(userID), enrollmentsvc.SubmitInput{
		LockToken:          req.LockToken,
		GroupID:            req.GroupID,
		FormData:           req.FormData,
		DisclaimerVersion:  req.DisclaimerVersion,
		DisclaimerSignedAt: req.DisclaimerSignedAt,
		ClientIP:           c.ClientIP(),
		UserAgent:          c.Request.UserAgent(),
	})
	if err != nil {
		switch {
		case errors.Is(err, enrollmentsvc.ErrInvalidArgument):
			logSubmitRejected(c, req.GroupID, "invalid_argument")
			response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "请求参数校验失败")
		case errors.Is(err, enrollmentsvc.ErrGroupNotFound):
			logSubmitRejected(c, req.GroupID, "group_not_found")
			response.Error(c, http.StatusNotFound, response.CodeGroupNotFound, "组别不存在")
		case errors.Is(err, enrollmentsvc.ErrEventNotOpen):
			logSubmitRejected(c, req.GroupID, "event_not_open")
			response.Error(c, http.StatusConflict, response.CodeEventNotOpen, eventNotOpenMessage(err))
		case errors.Is(err, enrollmentsvc.ErrLockInvalid):
			logSubmitRejected(c, req.GroupID, "lock_invalid")
			response.Error(c, http.StatusConflict, response.CodeLockInvalid, "锁定无效")
		case errors.Is(err, enrollmentsvc.ErrLockExpired):
			logSubmitRejected(c, req.GroupID, "lock_expired")
			response.Error(c, http.StatusConflict, response.CodeLockExpired, "锁定已过期")
		case errors.Is(err, enrollmentsvc.ErrLockAlreadyConsumed):
			logSubmitRejected(c, req.GroupID, "lock_already_consumed")
			response.Error(c, http.StatusConflict, response.CodeLockAlreadyConsumed, "锁定已被消费")
		case errors.Is(err, enrollmentsvc.ErrEnrollmentDuplicate):
			logSubmitRejected(c, req.GroupID, "enrollment_duplicate")
			response.Error(c, http.StatusConflict, response.CodeEnrollmentDuplicate, "请勿重复报名")
		case errors.Is(err, enrollmentsvc.ErrDisclaimerVersionMismatch):
			logSubmitRejected(c, req.GroupID, "disclaimer_version_mismatch")
			response.Error(c, http.StatusConflict, response.CodeDisclaimerVersionMismatch, "免责协议版本不一致")
		default:
			slog.ErrorContext(c.Request.Context(), "submit enrollment failed",
				"request_id", requestid.Get(c),
				"error_type", fmt.Sprintf("%T", err),
			)
			response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "服务器内部错误")
		}
		return
	}

	c.JSON(http.StatusCreated, SubmitResponse{
		Enrollment: EnrollmentResponse{
			ID:        result.Enrollment.ID,
			EventID:   result.Enrollment.EventID,
			GroupID:   result.Enrollment.GroupID,
			UserID:    result.Enrollment.UserID,
			OrderID:   result.Enrollment.OrderID,
			Status:    result.Enrollment.Status,
			CreatedAt: result.Enrollment.CreatedAt,
		},
		Order: OrderResponse{
			ID:           result.Order.ID,
			EnrollmentID: result.Order.EnrollmentID,
			Amount:       result.Order.Amount,
			Status:       result.Order.Status,
			PaidAt:       result.Order.PaidAt,
			CreatedAt:    result.Order.CreatedAt,
		},
	})
}

func logSubmitRejected(c *gin.Context, groupID, reason string) {
	slog.WarnContext(c.Request.Context(), "submit enrollment rejected",
		"request_id", requestid.Get(c),
		"group_id", groupID,
		"reason", reason,
	)
}

// ListMine 处理 GET /api/v1/enrollments/my（需 JWT）。
//
// 列表中的 form_data 默认脱敏返回（docs/constraints.md 第 19 条）。
func (h *Handler) ListMine(c *gin.Context) {
	if h.querySvc == nil {
		response.NotImplemented(c)
		return
	}

	userID, ok := c.Get(middleware.ContextUserID)
	if !ok || userID == "" {
		response.Error(c, http.StatusUnauthorized, response.CodeAuthInvalidToken, "缺少有效的登录身份")
		return
	}

	items, err := h.querySvc.ListMine(c.Request.Context(), fmt.Sprint(userID))
	if err != nil {
		if errors.Is(err, enrollmentsvc.ErrInvalidArgument) {
			response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "请求参数校验失败")
			return
		}
		slog.ErrorContext(c.Request.Context(), "list my enrollments failed",
			"request_id", requestid.Get(c),
			"error_type", fmt.Sprintf("%T", err),
		)
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "服务器内部错误")
		return
	}

	responses := make([]EnrollmentResponse, 0, len(items))
	for _, item := range items {
		responses = append(responses, enrollmentResponse(item, true))
	}
	c.JSON(http.StatusOK, ListMineResponse{Items: responses})
}

// Detail 处理 GET /api/v1/enrollments/:enrollment_id（需 JWT）。
//
// 仅允许报名本人访问；不存在或非本人统一返回 404 ENROLLMENT_NOT_FOUND。
func (h *Handler) Detail(c *gin.Context) {
	if h.querySvc == nil {
		response.NotImplemented(c)
		return
	}

	userID, ok := c.Get(middleware.ContextUserID)
	if !ok || userID == "" {
		response.Error(c, http.StatusUnauthorized, response.CodeAuthInvalidToken, "缺少有效的登录身份")
		return
	}

	item, err := h.querySvc.Detail(c.Request.Context(), fmt.Sprint(userID), c.Param("enrollment_id"))
	if err != nil {
		switch {
		case errors.Is(err, enrollmentsvc.ErrInvalidArgument):
			response.Error(c, http.StatusBadRequest, response.CodeInvalidArgument, "请求参数校验失败")
		case errors.Is(err, enrollmentsvc.ErrEnrollmentNotFound):
			response.Error(c, http.StatusNotFound, response.CodeEnrollmentNotFound, "报名不存在")
		default:
			slog.ErrorContext(c.Request.Context(), "get enrollment detail failed",
				"request_id", requestid.Get(c),
				"error_type", fmt.Sprintf("%T", err),
			)
			response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "服务器内部错误")
		}
		return
	}

	c.JSON(http.StatusOK, enrollmentResponse(*item, false))
}

// LockRequest 对应 openapi.yaml 中 /enrollments/lock 的请求体。
type LockRequest struct {
	GroupID string `json:"group_id" binding:"required"`
}

// LockResponse 对应 openapi.yaml 中锁定成功的响应体。
type LockResponse struct {
	LockToken      string    `json:"lock_token"`
	ExpiresAt      time.Time `json:"expires_at"`
	RemainingSlots int       `json:"remaining_slots"`
}

// SubmitRequest 对应 openapi.yaml 中 /enrollments 的请求体。
type SubmitRequest struct {
	LockToken          string         `json:"lock_token" binding:"required"`
	GroupID            string         `json:"group_id" binding:"required"`
	FormData           map[string]any `json:"form_data"`
	DisclaimerVersion  string         `json:"disclaimer_version" binding:"required"`
	DisclaimerSignedAt time.Time      `json:"disclaimer_signed_at" binding:"required"`
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

// ListMineResponse 对应 GET /enrollments/my 的响应体。
type ListMineResponse struct {
	Items []EnrollmentResponse `json:"items"`
}

// OrderResponse 对应 openapi.yaml 的 Order。
type OrderResponse struct {
	ID           string     `json:"id"`
	EnrollmentID string     `json:"enrollment_id"`
	Amount       int        `json:"amount"`
	Status       string     `json:"status"`
	PaidAt       *time.Time `json:"paid_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// SubmitResponse 对应 openapi.yaml 的 SubmitEnrollmentResponse。
type SubmitResponse struct {
	Enrollment EnrollmentResponse `json:"enrollment"`
	Order      OrderResponse      `json:"order"`
}

func enrollmentResponse(item model.Enrollment, mask bool) EnrollmentResponse {
	formData := item.FormData
	if mask {
		formData = maskFormData(formData)
	}
	return EnrollmentResponse{
		ID:        item.ID,
		EventID:   item.EventID,
		GroupID:   item.GroupID,
		UserID:    item.UserID,
		OrderID:   item.OrderID,
		Status:    item.Status,
		FormData:  formData,
		CreatedAt: item.CreatedAt,
	}
}

func maskFormData(data map[string]any) map[string]any {
	if data == nil {
		return nil
	}
	masked := make(map[string]any, len(data))
	for key, value := range data {
		if isSensitiveField(key) {
			masked[key] = "***"
			continue
		}
		if nested, ok := value.(map[string]any); ok {
			masked[key] = maskFormData(nested)
			continue
		}
		masked[key] = value
	}
	return masked
}

func isSensitiveField(key string) bool {
	normalized := strings.ToLower(key)
	for _, marker := range []string{
		"id_card", "idcard", "id_number", "idnumber",
		"phone", "mobile", "medical", "health",
		"passport", "bank_card", "bankcard", "email",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}
