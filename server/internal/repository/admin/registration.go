package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"bike-enroll/server/internal/model"
)

// CloseRegistration 将赛事报名置为手动关闭，并记录原因。
//
// 幂等：已关闭时重复调用仍会返回当前状态，并用最新 reason 覆盖（空则保留原值）。
func (r *GORMRepository) CloseRegistration(ctx context.Context, eventID, reason string, now time.Time) (*model.Event, error) {
	var event model.Event
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", eventID).First(&event).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrEventNotFound
			}
			return fmt.Errorf("lock event: %w", err)
		}

		trimmed := strings.TrimSpace(reason)
		updates := map[string]any{
			"registration_manually_closed": true,
			"updated_at":                   now,
		}
		if trimmed != "" {
			updates["registration_closed_reason"] = trimmed
		} else if event.RegistrationClosedReason == nil {
			updates["registration_closed_reason"] = "手动关闭"
		}
		if err := tx.Model(&model.Event{}).Where("id = ?", eventID).Updates(updates).Error; err != nil {
			return fmt.Errorf("close registration: %w", err)
		}

		event.RegistrationManuallyClosed = true
		event.UpdatedAt = now
		if trimmed != "" {
			event.RegistrationClosedReason = &trimmed
		} else if event.RegistrationClosedReason == nil {
			fallback := "手动关闭"
			event.RegistrationClosedReason = &fallback
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &event, nil
}

// OpenRegistration 取消手动关闭标记并清空原因。
//
// 若截止时间已过（deadline 非空且 now > deadline），返回 ErrRegistrationDeadlinePassed，
// 且不修改任何字段，保证失败调用不产生半成品状态。
func (r *GORMRepository) OpenRegistration(ctx context.Context, eventID string, now time.Time) (*model.Event, error) {
	var event model.Event
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", eventID).First(&event).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrEventNotFound
			}
			return fmt.Errorf("lock event: %w", err)
		}
		if event.RegistrationDeadline != nil && now.After(*event.RegistrationDeadline) {
			return ErrRegistrationDeadlinePassed
		}

		if err := tx.Model(&model.Event{}).Where("id = ?", eventID).Updates(map[string]any{
			"registration_manually_closed": false,
			"registration_closed_reason":   nil,
			"updated_at":                   now,
		}).Error; err != nil {
			return fmt.Errorf("open registration: %w", err)
		}

		event.RegistrationManuallyClosed = false
		event.RegistrationClosedReason = nil
		event.UpdatedAt = now
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &event, nil
}
