package enrollment

import (
	"fmt"
	"strings"
	"time"

	"bike-enroll/server/internal/model"
)

// EventNotOpenError 携带按关闭原因生成的可读消息。
//
// Unwrap 返回 ErrEventNotOpen，handler 可继续用 errors.Is 判断错误码。
type EventNotOpenError struct {
	Message string
}

// Error 返回可读消息。
func (e *EventNotOpenError) Error() string {
	return e.Message
}

// Unwrap 使 errors.Is(err, ErrEventNotOpen) 成立。
func (e *EventNotOpenError) Unwrap() error {
	return ErrEventNotOpen
}

// ensureRegistrationOpen 校验报名开关；非 open 时返回带原因的消息。
//
// 与 remaining_slots 的判断相互独立：调用方必须先过这里，再做名额判断。
func (s *Service) ensureRegistrationOpen(event *model.Event, now time.Time) error {
	if model.ResolveRegistrationStatus(event, now) == model.RegistrationOpen {
		return nil
	}
	return &EventNotOpenError{Message: registrationNotOpenMessage(event, now)}
}

// registrationNotOpenMessage 按 resolveRegistrationStatus 的优先级生成文案：
//   - 手动关闭：优先使用 registration_closed_reason，空则默认文案；
//   - 未开始：返回开放时间；
//   - 已截止：返回截止时间；
//   - 赛事取消：返回取消文案。
func registrationNotOpenMessage(event *model.Event, now time.Time) string {
	const defaultMessage = "赛事未开放报名"
	if event == nil {
		return defaultMessage
	}
	if event.RegistrationManuallyClosed {
		if event.RegistrationClosedReason != nil {
			if reason := strings.TrimSpace(*event.RegistrationClosedReason); reason != "" {
				return reason
			}
		}
		return defaultMessage
	}
	if event.RegistrationOpenAt != nil && now.Before(*event.RegistrationOpenAt) {
		return fmt.Sprintf("报名将于 %s 开放", formatGateTime(*event.RegistrationOpenAt))
	}
	if event.RegistrationDeadline != nil && now.After(*event.RegistrationDeadline) {
		return fmt.Sprintf("报名已于 %s 截止", formatGateTime(*event.RegistrationDeadline))
	}
	if event.LifecycleStatus == model.LifecycleCancelled {
		return "赛事已取消，报名已关闭"
	}
	return defaultMessage
}

func formatGateTime(t time.Time) string {
	return t.Format("2006-01-02 15:04")
}
