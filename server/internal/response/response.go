// Package response 定义统一的 HTTP 错误响应。
//
// 约束（docs/constraints.md 第 18 条）：错误响应统一为
// { code, message, request_id }。
package response

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"bike-enroll/server/internal/requestid"
)

// 业务错误码，与 docs/openapi.yaml 中 ErrorResponse.code 的枚举保持一致。
const (
	CodeAuthInvalidToken = "AUTH_INVALID_TOKEN"
	CodeAuthExpired      = "AUTH_EXPIRED"
	CodeAuthInvalidCode  = "AUTH_INVALID_CODE"
	CodeAuthForbidden    = "AUTH_FORBIDDEN"

	CodeInvalidArgument = "INVALID_ARGUMENT"

	CodeEventNotFound = "EVENT_NOT_FOUND"
	CodeEventNotOpen  = "EVENT_NOT_OPEN"

	CodeGroupNotFound = "GROUP_NOT_FOUND"
	CodeGroupSoldOut  = "GROUP_SOLD_OUT"
	CodeGroupClosed   = "GROUP_CLOSED"

	// 管理后台清理与名额重建相关错误码。
	CodeGroupInUse           = "GROUP_IN_USE"
	CodeEventHasGroups       = "EVENT_HAS_GROUPS"
	CodeGroupCounterExists   = "GROUP_COUNTER_EXISTS"
	CodeGroupReconcileFailed = "GROUP_RECONCILE_FAILED"
	// CodeRegistrationDeadlinePassed 表示报名截止时间已过，无法重新开放报名。
	CodeRegistrationDeadlinePassed = "REGISTRATION_DEADLINE_PASSED"
	// CodeInvalidTimeRange 表示赛事时间窗口不满足
	// registration_open_at < registration_deadline <= start_time < end_time。
	CodeInvalidTimeRange = "INVALID_TIME_RANGE"

	CodeLockInvalid         = "LOCK_INVALID"
	CodeLockExpired         = "LOCK_EXPIRED"
	CodeLockAlreadyConsumed = "LOCK_ALREADY_CONSUMED"

	CodeEnrollmentDuplicate = "ENROLLMENT_DUPLICATE"
	CodeEnrollmentNotFound  = "ENROLLMENT_NOT_FOUND"

	CodeOrderNotFound      = "ORDER_NOT_FOUND"
	CodeOrderStatusInvalid = "ORDER_STATUS_INVALID"

	CodePaySignInvalid    = "PAY_SIGN_INVALID"
	CodePayAmountMismatch = "PAY_AMOUNT_MISMATCH"

	CodeDisclaimerVersionMismatch = "DISCLAIMER_VERSION_MISMATCH"

	CodeRateLimited   = "RATE_LIMITED"
	CodeInternalError = "INTERNAL_ERROR"
)

// CodeNotImplemented 仅用于尚未实现的处理器占位，不属于 openapi.yaml 契约。
const CodeNotImplemented = "NOT_IMPLEMENTED"

// ErrorResponse 是统一错误响应体。
type ErrorResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

// Error 输出统一错误结构并终止后续 handler / 中间件。
func Error(c *gin.Context, httpStatus int, code, message string) {
	c.AbortWithStatusJSON(httpStatus, ErrorResponse{
		Code:      code,
		Message:   message,
		RequestID: requestid.Get(c),
	})
}

// NotImplemented 是未实现接口的占位响应。
func NotImplemented(c *gin.Context) {
	Error(c, http.StatusNotImplemented, CodeNotImplemented, "接口尚未实现")
}
