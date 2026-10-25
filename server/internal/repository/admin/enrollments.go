package admin

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"bike-enroll/server/internal/model"
)

// EnrollmentFilter 是后台报名名单筛选条件。
type EnrollmentFilter struct {
	EventID  string
	GroupID  string
	Status   string
	Page     int
	PageSize int
}

// EnrollmentPage 是报名名单分页结果。
type EnrollmentPage struct {
	Items []model.Enrollment
	Total int64
}

// GroupStats 是单个组别的统计口径。
type GroupStats struct {
	GroupID             string
	TotalSlots          int
	ActiveLockCount     int64
	PendingPaymentCount int64
	PaidCount           int64
	RemainingSlots      int
}

// StatsOverviewData 是赛事统计总览。
type StatsOverviewData struct {
	EventID string
	Groups  []GroupStats
}

// ListEnrollments 按赛事/组别/状态分页查询报名。
func (r *GORMRepository) ListEnrollments(ctx context.Context, filter EnrollmentFilter) (*EnrollmentPage, error) {
	buildQuery := func() *gorm.DB {
		query := r.db.WithContext(ctx).Model(&model.Enrollment{})
		if filter.EventID != "" {
			query = query.Where("event_id = ?", filter.EventID)
		}
		if filter.GroupID != "" {
			query = query.Where("group_id = ?", filter.GroupID)
		}
		if filter.Status != "" {
			query = query.Where("status = ?", filter.Status)
		}
		return query
	}

	var total int64
	if err := buildQuery().Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count enrollments: %w", err)
	}

	var items []model.Enrollment
	offset := (filter.Page - 1) * filter.PageSize
	if err := buildQuery().
		Order("created_at DESC, id DESC").
		Offset(offset).
		Limit(filter.PageSize).
		Find(&items).Error; err != nil {
		return nil, fmt.Errorf("list enrollments: %w", err)
	}
	return &EnrollmentPage{Items: items, Total: total}, nil
}

// StatsOverview 按对账等式口径统计赛事下全部组别。
func (r *GORMRepository) StatsOverview(ctx context.Context, eventID string) (*StatsOverviewData, error) {
	var eventCount int64
	if err := r.db.WithContext(ctx).Model(&model.Event{}).
		Where("id = ?", eventID).
		Count(&eventCount).Error; err != nil {
		return nil, fmt.Errorf("check stats event: %w", err)
	}
	if eventCount == 0 {
		return nil, ErrEventNotFound
	}

	var groups []model.Group
	if err := r.db.WithContext(ctx).
		Where("event_id = ?", eventID).
		Order("start_time ASC, id ASC").
		Find(&groups).Error; err != nil {
		return nil, fmt.Errorf("list stats groups: %w", err)
	}

	result := &StatsOverviewData{EventID: eventID, Groups: make([]GroupStats, 0, len(groups))}
	for _, group := range groups {
		remaining, err := r.rdb.Get(ctx, "group:"+group.ID+":remaining").Int()
		if errors.Is(err, redis.Nil) {
			return nil, ErrRemainingMissing
		}
		if err != nil {
			return nil, fmt.Errorf("read group remaining: %w", err)
		}

		stats := GroupStats{
			GroupID:        group.ID,
			TotalSlots:     group.TotalSlots,
			RemainingSlots: remaining,
		}
		if err := r.db.WithContext(ctx).Model(&model.EnrollmentLock{}).
			Where("group_id = ? AND status = ?", group.ID, "active").
			Count(&stats.ActiveLockCount).Error; err != nil {
			return nil, fmt.Errorf("count active locks: %w", err)
		}
		if err := r.db.WithContext(ctx).Model(&model.Enrollment{}).
			Where("group_id = ? AND status = ?", group.ID, "pending_payment").
			Count(&stats.PendingPaymentCount).Error; err != nil {
			return nil, fmt.Errorf("count pending enrollments: %w", err)
		}
		if err := r.db.WithContext(ctx).Model(&model.Enrollment{}).
			Where("group_id = ? AND status IN ?", group.ID, []string{"paid", "confirmed"}).
			Count(&stats.PaidCount).Error; err != nil {
			return nil, fmt.Errorf("count paid enrollments: %w", err)
		}
		result.Groups = append(result.Groups, stats)
	}
	return result, nil
}
