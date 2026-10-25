package admin

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"bike-enroll/server/internal/model"
)

// GroupDeleteResult 是组别删除结果。
type GroupDeleteResult struct {
	GroupID      string
	Locks        int64
	Enrollments  int64
	Orders       int64
	RedisDeleted bool
	RedisError   string
}

// DeleteGroup 删除没有任何报名/订单/锁的组别，并清理 Redis 计数 key。
//
// 只要存在 enrollment_locks、enrollments 或 orders 行就拒绝删除（ErrGroupInUse）：
// 硬删除这些历史会破坏报名可追溯性与对账口径，此类组别应保留或先按业务流程归档。
// MySQL 删除成功后才 DEL Redis key；DEL 失败只记录在结果里，由上层告警，
// 因为组别行已不存在，残留 key 不会被任何读取路径命中。
func (r *GORMRepository) DeleteGroup(ctx context.Context, groupID string) (*GroupDeleteResult, error) {
	result := &GroupDeleteResult{GroupID: groupID}

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var group model.Group
		// 锁住组别行，避免与名额调整、报名落库并发交错。
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", groupID).
			First(&group).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrGroupNotFound
			}
			return fmt.Errorf("lock group: %w", err)
		}

		if err := tx.Model(&model.EnrollmentLock{}).
			Where("group_id = ?", groupID).Count(&result.Locks).Error; err != nil {
			return fmt.Errorf("count group locks: %w", err)
		}
		if err := tx.Model(&model.Enrollment{}).
			Where("group_id = ?", groupID).Count(&result.Enrollments).Error; err != nil {
			return fmt.Errorf("count group enrollments: %w", err)
		}
		if err := tx.Model(&model.Order{}).
			Where("group_id = ?", groupID).Count(&result.Orders).Error; err != nil {
			return fmt.Errorf("count group orders: %w", err)
		}
		if result.Locks > 0 || result.Enrollments > 0 || result.Orders > 0 {
			return fmt.Errorf("%w: locks=%d enrollments=%d orders=%d",
				ErrGroupInUse, result.Locks, result.Enrollments, result.Orders)
		}

		if err := tx.Where("id = ?", groupID).Delete(&model.Group{}).Error; err != nil {
			return fmt.Errorf("delete group: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if err := r.rdb.Del(ctx, "group:"+groupID+":remaining").Err(); err != nil {
		result.RedisError = err.Error()
	} else {
		result.RedisDeleted = true
	}
	return result, nil
}
