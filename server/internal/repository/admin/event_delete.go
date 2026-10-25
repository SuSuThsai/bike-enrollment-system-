package admin

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"bike-enroll/server/internal/model"
)

// EventDeleteResult 是赛事删除结果。
type EventDeleteResult struct {
	EventID    string
	GroupCount int64
}

// DeleteEvent 删除没有任何组别的赛事。
//
// 赛事下仍有组别时拒绝删除（ErrEventHasGroups）：组别承载名额计数与报名数据，
// 必须先逐个删除空组别，避免出现"赛事已删、组别与 Redis 计数仍存活"的悬挂状态。
func (r *GORMRepository) DeleteEvent(ctx context.Context, eventID string) (*EventDeleteResult, error) {
	result := &EventDeleteResult{EventID: eventID}

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var event model.Event
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", eventID).
			First(&event).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrEventNotFound
			}
			return fmt.Errorf("lock event: %w", err)
		}

		if err := tx.Model(&model.Group{}).
			Where("event_id = ?", eventID).Count(&result.GroupCount).Error; err != nil {
			return fmt.Errorf("count event groups: %w", err)
		}
		if result.GroupCount > 0 {
			return fmt.Errorf("%w: groups=%d", ErrEventHasGroups, result.GroupCount)
		}

		if err := tx.Where("id = ?", eventID).Delete(&model.Event{}).Error; err != nil {
			return fmt.Errorf("delete event: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
