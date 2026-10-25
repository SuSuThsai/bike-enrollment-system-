package admin

import (
	"context"
	"fmt"
	"time"

	"bike-enroll/server/internal/model"
)

// 释放健康检查的默认阈值，与 docs/state-machine.md / docs/constraints.md 一致：
//   - 锁泄漏：status='active' 且 expires_at 已过期超过 1 分钟；
//   - 超时遗漏：订单 status='pending_payment' 且 created_at 超过 15 分钟。
const (
	LockLeakThreshold   = time.Minute
	PayTimeoutThreshold = 15 * time.Minute
)

// ReleaseHealthData 是一次释放健康检查（只读）的结果。
type ReleaseHealthData struct {
	EventID            string
	CheckedAt          time.Time
	LockOverdueSeconds int
	PayTimeoutMinutes  int
	Groups             []GroupReleaseHealth
}

// GroupReleaseHealth 是单个组别的释放健康指标。
type GroupReleaseHealth struct {
	GroupID             string
	GroupName           string
	LockLeakCount       int64
	MaxOverdueSeconds   int64
	PendingTimeoutCount int64
	LeakSamples         []LockLeakSample
	PendingSamples      []PendingTimeoutSample
}

// LockLeakSample 是锁泄漏样例。
type LockLeakSample struct {
	Token          string
	ExpiresAt      time.Time
	OverdueSeconds int64
}

// PendingTimeoutSample 是支付超时遗漏样例。
type PendingTimeoutSample struct {
	OrderID        string
	CreatedAt      time.Time
	OverdueMinutes int64
}

// ReleaseHealth 统计指定赛事下各组的「锁泄漏」与「支付超时遗漏」情况。
//
// 只读，不修改任何数据；阈值固定为文档口径（1 分钟 / 15 分钟）。
func (r *GORMRepository) ReleaseHealth(ctx context.Context, eventID string) (*ReleaseHealthData, error) {
	var eventCount int64
	if err := r.db.WithContext(ctx).Model(&model.Event{}).
		Where("id = ?", eventID).Count(&eventCount).Error; err != nil {
		return nil, fmt.Errorf("check release health event: %w", err)
	}
	if eventCount == 0 {
		return nil, ErrEventNotFound
	}

	var groups []model.Group
	if err := r.db.WithContext(ctx).
		Where("event_id = ?", eventID).
		Order("created_at ASC, id ASC").
		Find(&groups).Error; err != nil {
		return nil, fmt.Errorf("list groups for release health: %w", err)
	}

	lockSeconds := int(LockLeakThreshold / time.Second)
	payMinutes := int(PayTimeoutThreshold / time.Minute)
	now := time.Now()

	data := &ReleaseHealthData{
		EventID:            eventID,
		CheckedAt:          now,
		LockOverdueSeconds: lockSeconds,
		PayTimeoutMinutes:  payMinutes,
		Groups:             make([]GroupReleaseHealth, 0, len(groups)),
	}

	for _, group := range groups {
		item := GroupReleaseHealth{GroupID: group.ID, GroupName: group.Name}

		var lockAgg struct {
			LockCount  int64 `gorm:"column:lock_count"`
			MaxOverdue int64 `gorm:"column:max_overdue"`
		}
		lockSQL := fmt.Sprintf(`
			SELECT COUNT(*) AS lock_count,
			       COALESCE(MAX(TIMESTAMPDIFF(SECOND, expires_at, NOW(3))), 0) AS max_overdue
			  FROM enrollment_locks
			 WHERE group_id = ? AND status = 'active'
			   AND expires_at < NOW(3) - INTERVAL %d SECOND`, lockSeconds)
		if err := r.db.WithContext(ctx).Raw(lockSQL, group.ID).Scan(&lockAgg).Error; err != nil {
			return nil, fmt.Errorf("count lock leaks for group %s: %w", group.ID, err)
		}
		item.LockLeakCount = lockAgg.LockCount
		item.MaxOverdueSeconds = lockAgg.MaxOverdue

		if lockAgg.LockCount > 0 {
			sampleSQL := fmt.Sprintf(`
				SELECT token, expires_at,
				       TIMESTAMPDIFF(SECOND, expires_at, NOW(3)) AS overdue_seconds
				  FROM enrollment_locks
				 WHERE group_id = ? AND status = 'active'
				   AND expires_at < NOW(3) - INTERVAL %d SECOND
				 ORDER BY expires_at ASC
				 LIMIT 5`, lockSeconds)
			if err := r.db.WithContext(ctx).Raw(sampleSQL, group.ID).Scan(&item.LeakSamples).Error; err != nil {
				return nil, fmt.Errorf("sample lock leaks for group %s: %w", group.ID, err)
			}
		}

		var pendingAgg struct {
			PendingCount int64 `gorm:"column:pending_count"`
			MaxOverdue   int64 `gorm:"column:max_overdue"`
		}
		pendingSQL := fmt.Sprintf(`
			SELECT COUNT(*) AS pending_count,
			       COALESCE(MAX(TIMESTAMPDIFF(MINUTE, created_at, NOW(3))), 0) AS max_overdue
			  FROM orders
			 WHERE group_id = ? AND status = 'pending_payment'
			   AND created_at < NOW(3) - INTERVAL %d MINUTE`, payMinutes)
		if err := r.db.WithContext(ctx).Raw(pendingSQL, group.ID).Scan(&pendingAgg).Error; err != nil {
			return nil, fmt.Errorf("count pending timeouts for group %s: %w", group.ID, err)
		}
		item.PendingTimeoutCount = pendingAgg.PendingCount

		if pendingAgg.PendingCount > 0 {
			sampleSQL := fmt.Sprintf(`
				SELECT id AS order_id, created_at,
				       TIMESTAMPDIFF(MINUTE, created_at, NOW(3)) AS overdue_minutes
				  FROM orders
				 WHERE group_id = ? AND status = 'pending_payment'
				   AND created_at < NOW(3) - INTERVAL %d MINUTE
				 ORDER BY created_at ASC
				 LIMIT 5`, payMinutes)
			if err := r.db.WithContext(ctx).Raw(sampleSQL, group.ID).Scan(&item.PendingSamples).Error; err != nil {
				return nil, fmt.Errorf("sample pending timeouts for group %s: %w", group.ID, err)
			}
		}

		data.Groups = append(data.Groups, item)
	}
	return data, nil
}
