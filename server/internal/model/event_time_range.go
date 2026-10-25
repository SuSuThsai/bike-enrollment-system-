package model

import (
	"fmt"
	"time"
)

// TimeRangeError 表示赛事时间窗口不合法，Message 描述具体不成立的关系。
type TimeRangeError struct {
	Message string
}

// Error 返回可读消息。
func (e *TimeRangeError) Error() string {
	return e.Message
}

// ValidateEventTimeRange 校验报名与赛事时间窗口：
//
//	registration_open_at < registration_deadline <= start_time < end_time
//
// 截止时间允许等于赛事开始时间，但不能晚于它。
func ValidateEventTimeRange(openAt, deadline, startTime, endTime time.Time) error {
	if openAt.IsZero() || deadline.IsZero() || startTime.IsZero() || endTime.IsZero() {
		return &TimeRangeError{Message: "报名开放时间、报名截止时间、赛事开始时间与结束时间均必填"}
	}
	if !openAt.Before(deadline) {
		return &TimeRangeError{Message: "报名开放时间必须早于报名截止时间"}
	}
	if deadline.After(startTime) {
		return &TimeRangeError{Message: "报名截止时间不能晚于赛事开始时间"}
	}
	if !startTime.Before(endTime) {
		return &TimeRangeError{Message: "赛事开始时间必须早于赛事结束时间"}
	}
	return nil
}

// EventDurationMinutes 返回赛事持续分钟数（用于接口展示，不在前端重复计算）。
//
// 若数据异常导致结束不晚于开始，返回 0，避免出现负值。
func EventDurationMinutes(startTime, endTime time.Time) int {
	if !endTime.After(startTime) {
		return 0
	}
	return int(endTime.Sub(startTime) / time.Minute)
}

// FormatDuration 把分钟数格式化为“X 小时 Y 分”，供日志/调试使用。
func FormatDuration(minutes int) string {
	if minutes <= 0 {
		return "0 分"
	}
	return fmt.Sprintf("%d 小时 %d 分", minutes/60, minutes%60)
}
