// Package event 提供赛事与组别的数据访问。
package event

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"bike-enroll/server/internal/model"
)

// ErrEventNotFound 表示赛事不存在或不可公开访问。
var ErrEventNotFound = errors.New("event not found")

// publicLifecycleStatuses 是公开可见的生命周期状态（草稿/取消不对外）。
var publicLifecycleStatuses = []string{
	model.LifecyclePublished,
	model.LifecycleFinished,
}

// Repository 是赛事查询仓储接口。
type Repository interface {
	ListPublic(ctx context.Context, registrationStatus string, offset, limit int) ([]model.Event, int64, error)
	MinPrices(ctx context.Context, eventIDs []string) (map[string]int, error)
	GetDetail(ctx context.Context, eventID string) (*model.Event, []model.Group, error)
}

// SlotReader 读取组别的实时剩余名额。
type SlotReader interface {
	GetRemaining(ctx context.Context, groupID string) (remaining int, exists bool, err error)
}

// GORMRepository 是 Repository 的 MySQL/GORM 实现。
type GORMRepository struct {
	db *gorm.DB
}

// NewGORM 创建赛事仓储。
func NewGORM(db *gorm.DB) *GORMRepository {
	return &GORMRepository{db: db}
}

// ListPublic 返回分页的公开赛事及总数。
//
// registrationStatus 取值 not_started / open / closed；空串表示不筛选。
// SQL 条件与 model.ResolveRegistrationStatus 的优先级保持一致。
func (r *GORMRepository) ListPublic(ctx context.Context, registrationStatus string, offset, limit int) ([]model.Event, int64, error) {
	countQuery := r.publicQuery(ctx, registrationStatus)
	var total int64
	if err := countQuery.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count events: %w", err)
	}

	var events []model.Event
	query := r.publicQuery(ctx, registrationStatus)
	if err := query.
		Order("start_time ASC, id ASC").
		Offset(offset).
		Limit(limit).
		Find(&events).Error; err != nil {
		return nil, 0, fmt.Errorf("list events: %w", err)
	}
	return events, total, nil
}

// MinPrices 返回每个赛事所有组别中的最低价（分）。
func (r *GORMRepository) MinPrices(ctx context.Context, eventIDs []string) (map[string]int, error) {
	result := make(map[string]int, len(eventIDs))
	if len(eventIDs) == 0 {
		return result, nil
	}

	type minPriceRow struct {
		EventID string `gorm:"column:event_id"`
		Price   int    `gorm:"column:price"`
	}
	var rows []minPriceRow
	err := r.db.WithContext(ctx).
		Table("groups").
		Select("event_id, MIN(price) AS price").
		Where("event_id IN ?", eventIDs).
		Group("event_id").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("query min group price: %w", err)
	}

	for _, row := range rows {
		result[row.EventID] = row.Price
	}
	return result, nil
}

// GetDetail 返回公开赛事及其组别。
func (r *GORMRepository) GetDetail(ctx context.Context, eventID string) (*model.Event, []model.Group, error) {
	var event model.Event
	err := r.db.WithContext(ctx).
		Where("id = ? AND lifecycle_status IN ?", eventID, publicLifecycleStatuses).
		First(&event).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, ErrEventNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("find event: %w", err)
	}

	var groups []model.Group
	if err := r.db.WithContext(ctx).
		Where("event_id = ?", eventID).
		Order("start_time ASC, id ASC").
		Find(&groups).Error; err != nil {
		return nil, nil, fmt.Errorf("list event groups: %w", err)
	}
	return &event, groups, nil
}

func (r *GORMRepository) publicQuery(ctx context.Context, registrationStatus string) *gorm.DB {
	query := r.db.WithContext(ctx).Model(&model.Event{}).
		Where("lifecycle_status IN ?", publicLifecycleStatuses)

	switch registrationStatus {
	case model.RegistrationNotStarted.String():
		query = query.Where(
			"registration_manually_closed = ? AND registration_open_at IS NOT NULL AND registration_open_at > NOW(3)",
			false,
		)
	case model.RegistrationClosed.String():
		query = query.Where(`registration_manually_closed = 1
			OR ((registration_open_at IS NULL OR registration_open_at <= NOW(3))
				AND ((registration_deadline IS NOT NULL AND registration_deadline < NOW(3))
					OR lifecycle_status = ?))`, model.LifecycleCancelled)
	case model.RegistrationOpen.String():
		query = query.Where(`registration_manually_closed = 0
			AND (registration_open_at IS NULL OR registration_open_at <= NOW(3))
			AND (registration_deadline IS NULL OR registration_deadline >= NOW(3))
			AND lifecycle_status <> ?`, model.LifecycleCancelled)
	}
	return query
}
