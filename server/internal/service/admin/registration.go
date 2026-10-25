package admin

import (
	"context"
	"strings"
	"time"

	"bike-enroll/server/internal/model"
	"bike-enroll/server/internal/requestid"
)

// RegistrationState 是报名开关操作后的赛事状态。
type RegistrationState struct {
	Event  model.Event
	Status model.RegistrationStatus
}

// CloseEventRegistration 手动关闭赛事报名，并记录原因与审计日志。
//
// 幂等：已关闭时重复调用仍返回当前状态；不修改任何 enrollment / order / lock。
func (s *Service) CloseEventRegistration(ctx context.Context, adminID, eventID, reason string) (*RegistrationState, error) {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return nil, ErrInvalidArgument
	}
	event, err := s.repo.CloseRegistration(ctx, eventID, reason, time.Now())
	if err != nil {
		return nil, err
	}
	state := &RegistrationState{
		Event:  *event,
		Status: model.ResolveRegistrationStatus(event, time.Now()),
	}
	attrs := []any{
		"request_id", requestid.FromContext(ctx),
		"admin_id", adminID,
		"event_id", event.ID,
		"registration_status", state.Status,
	}
	if event.RegistrationClosedReason != nil {
		attrs = append(attrs, "reason", *event.RegistrationClosedReason)
	}
	s.logger.InfoContext(ctx, "admin close event registration", attrs...)
	return state, nil
}

// OpenEventRegistration 取消手动关闭标记，重新计算报名状态。
//
// 截止时间已过时由仓储返回 adminrepo.ErrRegistrationDeadlinePassed，
// 且不修改数据库；不修改任何 enrollment / order / lock。
func (s *Service) OpenEventRegistration(ctx context.Context, adminID, eventID string) (*RegistrationState, error) {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return nil, ErrInvalidArgument
	}
	event, err := s.repo.OpenRegistration(ctx, eventID, time.Now())
	if err != nil {
		return nil, err
	}
	state := &RegistrationState{
		Event:  *event,
		Status: model.ResolveRegistrationStatus(event, time.Now()),
	}
	s.logger.InfoContext(ctx, "admin open event registration",
		"request_id", requestid.FromContext(ctx),
		"admin_id", adminID,
		"event_id", event.ID,
		"registration_status", state.Status,
	)
	return state, nil
}
