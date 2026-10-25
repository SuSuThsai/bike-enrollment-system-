package admin

import (
	"context"
	"strings"

	adminrepo "bike-enroll/server/internal/repository/admin"
	"bike-enroll/server/internal/requestid"
)

// DeleteEvent 删除空赛事，并记录审计日志。
//
// 赛事下仍有组别时由仓储返回 adminrepo.ErrEventHasGroups，不做级联删除。
func (s *Service) DeleteEvent(ctx context.Context, adminID, eventID string) (*adminrepo.EventDeleteResult, error) {
	if strings.TrimSpace(eventID) == "" {
		return nil, ErrInvalidArgument
	}
	result, err := s.repo.DeleteEvent(ctx, eventID)
	if err != nil {
		return nil, err
	}
	s.logger.InfoContext(ctx, "admin delete event",
		"request_id", requestid.FromContext(ctx),
		"admin_id", adminID,
		"event_id", result.EventID,
		"group_count", result.GroupCount,
	)
	return result, nil
}

// DeleteGroup 删除没有任何锁/报名/订单的组别，并清理 Redis 计数 key。
func (s *Service) DeleteGroup(ctx context.Context, adminID, groupID string) (*adminrepo.GroupDeleteResult, error) {
	if strings.TrimSpace(groupID) == "" {
		return nil, ErrInvalidArgument
	}
	result, err := s.repo.DeleteGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	s.logger.InfoContext(ctx, "admin delete group",
		"request_id", requestid.FromContext(ctx),
		"admin_id", adminID,
		"group_id", result.GroupID,
		"locks", result.Locks,
		"enrollments", result.Enrollments,
		"orders", result.Orders,
		"redis_key_deleted", result.RedisDeleted,
	)
	if result.RedisError != "" {
		// MySQL 行已删除，残留 key 不会被读取路径命中，但必须留痕便于排查。
		s.logger.ErrorContext(ctx, "admin delete group: redis key cleanup failed",
			"request_id", requestid.FromContext(ctx),
			"admin_id", adminID,
			"group_id", result.GroupID,
			"error", result.RedisError,
		)
	}
	return result, nil
}

// RebuildGroupRemaining 按对账等式重建组别名额计数器，并记录审计日志。
//
// 冲突（key 已存在且取值与等式不一致）不是错误，由 handler 依据 Mode 返回 409，
// 这里额外打一条告警日志，便于和 PUT /admin/groups 的调整记录对照排查。
func (s *Service) RebuildGroupRemaining(ctx context.Context, adminID, groupID string, force bool) (*adminrepo.RemainingRebuildResult, error) {
	if strings.TrimSpace(groupID) == "" {
		return nil, ErrInvalidArgument
	}
	result, err := s.repo.RebuildRemaining(ctx, groupID, force)
	if err != nil {
		return nil, err
	}

	attrs := []any{
		"request_id", requestid.FromContext(ctx),
		"admin_id", adminID,
		"group_id", result.GroupID,
		"force", force,
		"mode", result.Mode,
		"total_slots", result.TotalSlots,
		"expected_remaining", result.Expected,
		"active_locks", result.ActiveLocks,
		"pending_payment", result.PendingPayment,
		"paid_confirmed", result.PaidConfirmed,
	}
	if result.HasPrevious {
		attrs = append(attrs, "previous_remaining", result.Previous)
	}
	s.logger.InfoContext(ctx, "admin rebuild group remaining", attrs...)

	if result.Mode == "conflict" {
		s.logger.WarnContext(ctx, "admin rebuild group remaining conflict",
			"request_id", requestid.FromContext(ctx),
			"admin_id", adminID,
			"group_id", result.GroupID,
			"previous_remaining", result.Previous,
			"expected_remaining", result.Expected,
		)
	}
	return result, nil
}
