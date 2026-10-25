package admin

import (
	"context"
	"strings"

	adminrepo "bike-enroll/server/internal/repository/admin"
	"bike-enroll/server/internal/requestid"
)

// ReleaseHealth 返回赛事下各组的「锁泄漏 / 支付超时遗漏」诊断结果（只读）。
//
// 阈值与 docs/state-machine.md、docs/constraints.md 一致：
// 锁过期超过 1 分钟仍为 active；订单 pending_payment 超过 15 分钟未释放。
func (s *Service) ReleaseHealth(ctx context.Context, eventID string) (*adminrepo.ReleaseHealthData, error) {
	if strings.TrimSpace(eventID) == "" {
		return nil, ErrInvalidArgument
	}
	result, err := s.repo.ReleaseHealth(ctx, eventID)
	if err != nil {
		return nil, err
	}

	var leaks, pending int64
	for _, group := range result.Groups {
		leaks += group.LockLeakCount
		pending += group.PendingTimeoutCount
	}
	s.logger.InfoContext(ctx, "admin release health check",
		"request_id", requestid.FromContext(ctx),
		"event_id", eventID,
		"groups", len(result.Groups),
		"lock_leaks", leaks,
		"pending_timeouts", pending,
	)
	return result, nil
}
