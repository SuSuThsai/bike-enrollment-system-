// Package lock 提供名额锁定所需的数据访问：MySQL 锁记录与 Redis 名额操作。
package lock

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"bike-enroll/server/internal/model"
)

// ErrGroupNotFound 表示组别或其所属赛事不存在。
var ErrGroupNotFound = errors.New("group not found")

// Repository 是锁记录持久化接口。
type Repository interface {
	GetGroupForLock(ctx context.Context, groupID string) (*model.Group, *model.Event, error)
	Create(ctx context.Context, lock *model.EnrollmentLock) error
}

// GORMRepository 是 Repository 的 MySQL/GORM 实现。
type GORMRepository struct {
	db *gorm.DB
}

// NewGORM 创建锁记录仓储。
func NewGORM(db *gorm.DB) *GORMRepository {
	return &GORMRepository{db: db}
}

// GetGroupForLock 返回组别及其所属赛事。
func (r *GORMRepository) GetGroupForLock(ctx context.Context, groupID string) (*model.Group, *model.Event, error) {
	var group model.Group
	if err := r.db.WithContext(ctx).Where("id = ?", groupID).First(&group).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrGroupNotFound
		}
		return nil, nil, fmt.Errorf("find group: %w", err)
	}

	var event model.Event
	if err := r.db.WithContext(ctx).Where("id = ?", group.EventID).First(&event).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrGroupNotFound
		}
		return nil, nil, fmt.Errorf("find group event: %w", err)
	}
	return &group, &event, nil
}

// Create 插入 active 锁记录。
func (r *GORMRepository) Create(ctx context.Context, lock *model.EnrollmentLock) error {
	if err := r.db.WithContext(ctx).Create(lock).Error; err != nil {
		return fmt.Errorf("create enrollment lock: %w", err)
	}
	return nil
}
