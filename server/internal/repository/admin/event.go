package admin

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"bike-enroll/server/internal/model"
)

// CreateEventParams 是创建赛事参数。
type CreateEventParams struct {
	Title                string
	CoverURL             string
	RegistrationOpenAt   time.Time
	RegistrationDeadline time.Time
	StartTime            time.Time
	EndTime              time.Time
	Location             string
	Description          string
	Rules                string
	Now                  time.Time
}

// UpdateEventParams 是赛事更新参数；nil 字段表示不修改。
type UpdateEventParams struct {
	EventID              string
	Title                *string
	CoverURL             *string
	RegistrationOpenAt   *time.Time
	RegistrationDeadline *time.Time
	StartTime            *time.Time
	EndTime              *time.Time
	Location             *string
	Description          *string
	Rules                *string
	Now                  time.Time
}

// GroupWithRemaining 是组别与 Redis 剩余名额。
type GroupWithRemaining struct {
	Group     model.Group
	Remaining int
}

// EventDetailData 是赛事详情数据。
type EventDetailData struct {
	Event  *model.Event
	Groups []GroupWithRemaining
}

// CreateEvent 创建赛事，初始生命周期为 published（报名默认开放）。
func (r *GORMRepository) CreateEvent(ctx context.Context, params CreateEventParams) (*model.Event, error) {
	if err := model.ValidateEventTimeRange(
		params.RegistrationOpenAt, params.RegistrationDeadline, params.StartTime, params.EndTime,
	); err != nil {
		return nil, err
	}
	event := &model.Event{
		ID:                   uuid.NewString(),
		Title:                params.Title,
		CoverURL:             params.CoverURL,
		RegistrationOpenAt:   &params.RegistrationOpenAt,
		RegistrationDeadline: &params.RegistrationDeadline,
		StartTime:            params.StartTime,
		EndTime:              params.EndTime,
		Location:             params.Location,
		LifecycleStatus:      model.LifecyclePublished,
		Description:          params.Description,
		Rules:                params.Rules,
		CreatedAt:            params.Now,
		UpdatedAt:            params.Now,
	}
	if err := r.db.WithContext(ctx).Create(event).Error; err != nil {
		return nil, fmt.Errorf("create event: %w", err)
	}
	return event, nil
}

// UpdateEvent 更新赛事字段。
func (r *GORMRepository) UpdateEvent(ctx context.Context, params UpdateEventParams) (*model.Event, error) {
	var event model.Event
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", params.EventID).
			First(&event).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrEventNotFound
			}
			return fmt.Errorf("lock event: %w", err)
		}

		updates := map[string]any{}
		if params.Title != nil {
			event.Title = *params.Title
			updates["title"] = *params.Title
		}
		if params.CoverURL != nil {
			event.CoverURL = *params.CoverURL
			updates["cover_url"] = *params.CoverURL
		}
		if params.StartTime != nil {
			event.StartTime = *params.StartTime
			updates["start_time"] = *params.StartTime
		}
		if params.EndTime != nil {
			event.EndTime = *params.EndTime
			updates["end_time"] = *params.EndTime
		}
		if params.RegistrationOpenAt != nil {
			event.RegistrationOpenAt = params.RegistrationOpenAt
			updates["registration_open_at"] = *params.RegistrationOpenAt
		}
		if params.RegistrationDeadline != nil {
			event.RegistrationDeadline = params.RegistrationDeadline
			updates["registration_deadline"] = *params.RegistrationDeadline
		}
		if params.Location != nil {
			event.Location = *params.Location
			updates["location"] = *params.Location
		}
		if params.Description != nil {
			event.Description = *params.Description
			updates["description"] = *params.Description
		}
		if params.Rules != nil {
			event.Rules = *params.Rules
			updates["rules"] = *params.Rules
		}

		var openAt, deadline time.Time
		if event.RegistrationOpenAt != nil {
			openAt = *event.RegistrationOpenAt
		}
		if event.RegistrationDeadline != nil {
			deadline = *event.RegistrationDeadline
		}
		if err := model.ValidateEventTimeRange(openAt, deadline, event.StartTime, event.EndTime); err != nil {
			return err
		}

		if len(updates) > 0 {
			event.UpdatedAt = params.Now
			updates["updated_at"] = params.Now
			if err := tx.Model(&model.Event{}).
				Where("id = ?", event.ID).
				Updates(updates).Error; err != nil {
				return fmt.Errorf("update event: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &event, nil
}

// EventDetail 返回赛事和全部组别的实时剩余名额。
func (r *GORMRepository) EventDetail(ctx context.Context, eventID string) (*EventDetailData, error) {
	var event model.Event
	if err := r.db.WithContext(ctx).Where("id = ?", eventID).First(&event).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrEventNotFound
		}
		return nil, fmt.Errorf("get event: %w", err)
	}

	var groups []model.Group
	if err := r.db.WithContext(ctx).
		Where("event_id = ?", eventID).
		Order("start_time ASC, id ASC").
		Find(&groups).Error; err != nil {
		return nil, fmt.Errorf("list event groups: %w", err)
	}

	keys := make([]string, 0, len(groups))
	for _, group := range groups {
		keys = append(keys, "group:"+group.ID+":remaining")
	}
	var values []any
	if len(keys) > 0 {
		var err error
		values, err = r.rdb.MGet(ctx, keys...).Result()
		if err != nil {
			return nil, fmt.Errorf("read group remainings: %w", err)
		}
	}

	result := &EventDetailData{Event: &event, Groups: make([]GroupWithRemaining, 0, len(groups))}
	for index, group := range groups {
		if index >= len(values) || values[index] == nil {
			return nil, fmt.Errorf("%w: group %s", ErrRemainingMissing, group.ID)
		}
		remaining, err := strconv.Atoi(fmt.Sprint(values[index]))
		if err != nil {
			return nil, fmt.Errorf("parse group %s remaining: %w", group.ID, err)
		}
		if remaining < 0 {
			return nil, fmt.Errorf("%w: group %s has negative remaining", ErrRemainingMissing, group.ID)
		}
		result.Groups = append(result.Groups, GroupWithRemaining{Group: group, Remaining: remaining})
	}
	return result, nil
}
