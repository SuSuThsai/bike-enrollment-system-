package admin

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"bike-enroll/server/internal/model"
)

// RemainingRebuildResult 是按对账等式重建名额的结果。
type RemainingRebuildResult struct {
	GroupID        string
	TotalSlots     int
	Expected       int
	Previous       int
	HasPrevious    bool
	Mode           string // created / overwritten / unchanged / conflict
	ActiveLocks    int64
	PendingPayment int64
	PaidConfirmed  int64
}

// RebuildRemaining 按 docs/state-machine.md 的对账等式反推剩余名额并写回 Redis。
//
// expected = total_slots - active locks - pending_payment - paid/confirmed。
// 反推结果为负说明数据本身已经不满足等式，返回 ErrReconcileFailed，不修改 Redis。
// 默认仅在 key 缺失时写入；force=true 才允许覆盖已存在的计数（漂移修复）。
func (r *GORMRepository) RebuildRemaining(ctx context.Context, groupID string, force bool) (*RemainingRebuildResult, error) {
	result := &RemainingRebuildResult{GroupID: groupID}

	var group model.Group
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 锁住组别行，避免与 PUT /admin/groups 并发调整 total_slots 交错。
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", groupID).
			First(&group).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrGroupNotFound
			}
			return fmt.Errorf("lock group: %w", err)
		}
		if err := tx.Model(&model.EnrollmentLock{}).
			Where("group_id = ? AND status = ?", groupID, "active").
			Count(&result.ActiveLocks).Error; err != nil {
			return fmt.Errorf("count active locks: %w", err)
		}
		if err := tx.Model(&model.Enrollment{}).
			Where("group_id = ? AND status = ?", groupID, "pending_payment").
			Count(&result.PendingPayment).Error; err != nil {
			return fmt.Errorf("count pending enrollments: %w", err)
		}
		if err := tx.Model(&model.Enrollment{}).
			Where("group_id = ? AND status IN ?", groupID, []string{"paid", "confirmed"}).
			Count(&result.PaidConfirmed).Error; err != nil {
			return fmt.Errorf("count paid enrollments: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	result.TotalSlots = group.TotalSlots
	result.Expected = group.TotalSlots - int(result.ActiveLocks+result.PendingPayment+result.PaidConfirmed)
	if result.Expected < 0 {
		return nil, fmt.Errorf("%w: total_slots=%d 已占用=%d",
			ErrReconcileFailed, group.TotalSlots,
			result.ActiveLocks+result.PendingPayment+result.PaidConfirmed)
	}

	forceFlag := "0"
	if force {
		forceFlag = "1"
	}
	values, err := r.restoreScript.Run(
		ctx,
		r.rdb,
		[]string{"group:" + groupID + ":remaining"},
		result.Expected,
		forceFlag,
	).Slice()
	if err != nil {
		return nil, fmt.Errorf("restore group remaining: %w", err)
	}

	mode := int64(0)
	if len(values) > 0 {
		if parsed, ok := values[0].(int64); ok {
			mode = parsed
		}
	}
	if len(values) > 1 {
		if previous, ok := values[1].(string); ok && previous != "" {
			var parsed int
			if _, scanErr := fmt.Sscan(previous, &parsed); scanErr == nil {
				result.Previous = parsed
				result.HasPrevious = true
			}
		}
	}

	switch mode {
	case 1:
		result.Mode = "created"
	case 2:
		result.Mode = "overwritten"
	case 3:
		result.Mode = "unchanged"
	default:
		result.Mode = "conflict"
	}
	return result, nil
}
