package admin

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"bike-enroll/server/internal/model"
)

// CreateGroupParams 是创建组别参数。
type CreateGroupParams struct {
	EventID           string
	Name              string
	Price             int
	TotalSlots        int
	StartTime         time.Time
	FormSchema        map[string]any
	DisclaimerVersion string
	Now               time.Time
}

// CreateGroup 创建组别，并在 Redis 初始化剩余名额；Redis 失败时回滚 MySQL。
func (r *GORMRepository) CreateGroup(ctx context.Context, params CreateGroupParams) (*GroupUpdateResult, error) {
	var eventCount int64
	if err := r.db.WithContext(ctx).Model(&model.Event{}).
		Where("id = ?", params.EventID).
		Count(&eventCount).Error; err != nil {
		return nil, fmt.Errorf("check group event: %w", err)
	}
	if eventCount == 0 {
		return nil, ErrEventNotFound
	}

	group := &model.Group{
		ID:                uuid.NewString(),
		EventID:           params.EventID,
		Name:              params.Name,
		Price:             params.Price,
		TotalSlots:        params.TotalSlots,
		StartTime:         params.StartTime,
		Status:            "open",
		FormSchema:        params.FormSchema,
		DisclaimerVersion: params.DisclaimerVersion,
		CreatedAt:         params.Now,
		UpdatedAt:         params.Now,
	}
	if err := r.db.WithContext(ctx).Create(group).Error; err != nil {
		return nil, fmt.Errorf("create group: %w", err)
	}

	if err := r.rdb.Set(ctx, "group:"+group.ID+":remaining", group.TotalSlots, 0).Err(); err != nil {
		if deleteErr := r.db.WithContext(ctx).Delete(group).Error; deleteErr != nil {
			return nil, fmt.Errorf("initialize group remaining: %w; rollback group failed: %v", err, deleteErr)
		}
		return nil, fmt.Errorf("initialize group remaining: %w", err)
	}
	return &GroupUpdateResult{Group: group, RemainingSlots: group.TotalSlots}, nil
}
