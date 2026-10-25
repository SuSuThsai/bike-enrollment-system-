package model

import "time"

// RegistrationStatus 是赛事报名状态的推导结果，不落库。
type RegistrationStatus string

const (
	// RegistrationNotStarted 表示尚未到报名开始时间。
	RegistrationNotStarted RegistrationStatus = "not_started"
	// RegistrationOpen 表示当前可报名。
	RegistrationOpen RegistrationStatus = "open"
	// RegistrationClosed 表示报名已关闭（手动关闭 / 过期 / 赛事取消）。
	RegistrationClosed RegistrationStatus = "closed"
)

// String 返回状态字面量。
func (s RegistrationStatus) String() string {
	return string(s)
}

// Valid 判断是否为已定义的报名状态。
func (s RegistrationStatus) Valid() bool {
	switch s {
	case RegistrationNotStarted, RegistrationOpen, RegistrationClosed:
		return true
	default:
		return false
	}
}

// ResolveRegistrationStatus 按固定优先级推导赛事报名状态：
//
//  1. registration_manually_closed = true            -> closed
//  2. now < registration_open_at                     -> not_started
//  3. now > registration_deadline                    -> closed
//  4. lifecycle_status = cancelled                   -> closed
//  5. 其余                                           -> open
//
// registration_open_at / registration_deadline 为 nil 表示该侧不限制；
// now 与 expires 边界相等时不算过期（严格大于才关闭）。
func ResolveRegistrationStatus(event *Event, now time.Time) RegistrationStatus {
	if event == nil {
		return RegistrationClosed
	}
	if event.RegistrationManuallyClosed {
		return RegistrationClosed
	}
	if event.RegistrationOpenAt != nil && now.Before(*event.RegistrationOpenAt) {
		return RegistrationNotStarted
	}
	if event.RegistrationDeadline != nil && now.After(*event.RegistrationDeadline) {
		return RegistrationClosed
	}
	if event.LifecycleStatus == LifecycleCancelled {
		return RegistrationClosed
	}
	return RegistrationOpen
}
