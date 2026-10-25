// Package admin 提供管理后台的组别名额调整与报名导出数据访问。
package admin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"bike-enroll/server/internal/model"
	"bike-enroll/server/internal/pkg/lua"
)

// 管理后台相关错误。
var (
	ErrGroupNotFound    = errors.New("group not found")
	ErrEventNotFound    = errors.New("event not found")
	ErrGroupSoldOut     = errors.New("group sold out")
	ErrRemainingMissing = errors.New("group remaining missing")

	// ErrGroupInUse 表示组别仍有锁、报名或订单，禁止删除。
	ErrGroupInUse = errors.New("group in use")
	// ErrEventHasGroups 表示赛事下仍有组别，禁止删除。
	ErrEventHasGroups = errors.New("event still has groups")
	// ErrReconcileFailed 表示按对账等式反推出的剩余名额为负，不能写入 Redis。
	ErrReconcileFailed = errors.New("reconcile equation cannot be satisfied")
	// ErrRegistrationDeadlinePassed 表示报名截止时间已过，无法重新开放。
	ErrRegistrationDeadlinePassed = errors.New("registration deadline passed")
)

// UpdateGroupParams 是组别更新参数；nil 字段表示不修改。
type UpdateGroupParams struct {
	GroupID           string
	Name              *string
	Price             *int
	TotalSlots        *int
	StartTime         *time.Time
	FormSchema        *map[string]any
	DisclaimerVersion *string
	Now               time.Time
}

// GroupUpdateResult 是组别更新后的响应数据。
type GroupUpdateResult struct {
	Group          *model.Group
	RemainingSlots int
}

// Repository 是管理后台数据访问接口。
type Repository interface {
	CreateEvent(ctx context.Context, params CreateEventParams) (*model.Event, error)
	UpdateEvent(ctx context.Context, params UpdateEventParams) (*model.Event, error)
	EventDetail(ctx context.Context, eventID string) (*EventDetailData, error)
	DeleteEvent(ctx context.Context, eventID string) (*EventDeleteResult, error)
	CreateGroup(ctx context.Context, params CreateGroupParams) (*GroupUpdateResult, error)
	UpdateGroup(ctx context.Context, params UpdateGroupParams) (*GroupUpdateResult, error)
	DeleteGroup(ctx context.Context, groupID string) (*GroupDeleteResult, error)
	RebuildRemaining(ctx context.Context, groupID string, force bool) (*RemainingRebuildResult, error)
	ListEnrollments(ctx context.Context, filter EnrollmentFilter) (*EnrollmentPage, error)
	ExportEnrollments(ctx context.Context, filter ExportFilter) (*ExportData, error)
	StatsOverview(ctx context.Context, eventID string) (*StatsOverviewData, error)
	ReleaseHealth(ctx context.Context, eventID string) (*ReleaseHealthData, error)
	CloseRegistration(ctx context.Context, eventID, reason string, now time.Time) (*model.Event, error)
	OpenRegistration(ctx context.Context, eventID string, now time.Time) (*model.Event, error)
}

// GORMRepository 是 Repository 的 MySQL/Redis 实现。
type GORMRepository struct {
	db            *gorm.DB
	rdb           *redis.Client
	slotScript    *redis.Script
	restoreScript *redis.Script
}

// NewGORM 创建管理后台仓储。
func NewGORM(db *gorm.DB, rdb *redis.Client) *GORMRepository {
	return &GORMRepository{
		db:            db,
		rdb:           rdb,
		slotScript:    redis.NewScript(lua.GroupSlotsScript),
		restoreScript: redis.NewScript(lua.GroupRestoreScript),
	}
}

// UpdateGroup 锁定组别行，原子调整 Redis 剩余名额，再提交 MySQL。
func (r *GORMRepository) UpdateGroup(ctx context.Context, params UpdateGroupParams) (*GroupUpdateResult, error) {
	var (
		group        model.Group
		remaining    int
		delta        int
		redisApplied bool
	)

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", params.GroupID).
			First(&group).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrGroupNotFound
			}
			return fmt.Errorf("lock group: %w", err)
		}

		if params.TotalSlots != nil {
			delta = *params.TotalSlots - group.TotalSlots
		}
		var err error
		remaining, err = r.applyDelta(ctx, group.ID, delta)
		if err != nil {
			return err
		}
		redisApplied = true

		updates := map[string]any{}
		if params.Name != nil {
			group.Name = *params.Name
			updates["name"] = *params.Name
		}
		if params.Price != nil {
			group.Price = *params.Price
			updates["price"] = *params.Price
		}
		if params.TotalSlots != nil {
			group.TotalSlots = *params.TotalSlots
			updates["total_slots"] = *params.TotalSlots
		}
		if params.StartTime != nil {
			group.StartTime = *params.StartTime
			updates["start_time"] = *params.StartTime
		}
		if params.FormSchema != nil {
			group.FormSchema = *params.FormSchema
			updates["form_schema"] = *params.FormSchema
		}
		if params.DisclaimerVersion != nil {
			group.DisclaimerVersion = *params.DisclaimerVersion
			updates["disclaimer_version"] = *params.DisclaimerVersion
		}
		if len(updates) > 0 {
			group.UpdatedAt = params.Now
			updates["updated_at"] = params.Now
			if err := tx.Model(&model.Group{}).
				Where("id = ?", group.ID).
				Updates(updates).Error; err != nil {
				return fmt.Errorf("update group: %w", err)
			}
		}
		return nil
	})

	if err != nil {
		if redisApplied && delta != 0 {
			rollbackCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if _, rollbackErr := r.applyDelta(rollbackCtx, params.GroupID, -delta); rollbackErr != nil {
				return nil, fmt.Errorf("%w; rollback redis failed: %v", err, rollbackErr)
			}
		}
		return nil, err
	}
	return &GroupUpdateResult{Group: &group, RemainingSlots: remaining}, nil
}

func (r *GORMRepository) applyDelta(ctx context.Context, groupID string, delta int) (int, error) {
	result, err := r.slotScript.Run(
		ctx,
		r.rdb,
		[]string{"group:" + groupID + ":remaining"},
		delta,
	).Int64()
	if err != nil {
		return 0, fmt.Errorf("adjust group remaining: %w", err)
	}
	switch result {
	case -1:
		return 0, ErrGroupSoldOut
	case -2:
		return 0, ErrRemainingMissing
	default:
		return int(result), nil
	}
}
