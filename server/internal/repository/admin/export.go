package admin

import (
	"context"
	"fmt"
	"time"

	"bike-enroll/server/internal/model"
)

// ExportFilter 是报名导出过滤条件。
type ExportFilter struct {
	EventID string
	GroupID string
	Fields  []string
	Limit   int
}

// ExportRow 是导出的一行报名数据。
type ExportRow struct {
	EnrollmentID string
	UserID       string
	EventID      string
	GroupID      string
	OrderID      string
	Status       string
	CreatedAt    time.Time
	FormData     map[string]any
}

// ExportData 是导出查询结果。
type ExportData struct {
	Rows      []ExportRow
	Total     int64
	Truncated bool
}

// ExportEnrollments 查询最多 limit+1 行，用于判断是否发生截断。
func (r *GORMRepository) ExportEnrollments(ctx context.Context, filter ExportFilter) (*ExportData, error) {
	var eventCount int64
	if err := r.db.WithContext(ctx).Model(&model.Event{}).
		Where("id = ?", filter.EventID).
		Count(&eventCount).Error; err != nil {
		return nil, fmt.Errorf("check export event: %w", err)
	}
	if eventCount == 0 {
		return nil, ErrEventNotFound
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 10000
	}

	query := r.db.WithContext(ctx).Model(&model.Enrollment{}).
		Where("event_id = ?", filter.EventID)
	if filter.GroupID != "" {
		query = query.Where("group_id = ?", filter.GroupID)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count export enrollments: %w", err)
	}

	var enrollments []model.Enrollment
	if err := query.Order("created_at ASC, id ASC").
		Limit(limit + 1).
		Find(&enrollments).Error; err != nil {
		return nil, fmt.Errorf("query export enrollments: %w", err)
	}

	truncated := len(enrollments) > limit
	if truncated {
		enrollments = enrollments[:limit]
	}
	rows := make([]ExportRow, 0, len(enrollments))
	for _, enrollment := range enrollments {
		rows = append(rows, ExportRow{
			EnrollmentID: enrollment.ID,
			UserID:       enrollment.UserID,
			EventID:      enrollment.EventID,
			GroupID:      enrollment.GroupID,
			OrderID:      enrollment.OrderID,
			Status:       enrollment.Status,
			CreatedAt:    enrollment.CreatedAt,
			FormData:     enrollment.FormData,
		})
	}
	return &ExportData{Rows: rows, Total: total, Truncated: truncated}, nil
}
