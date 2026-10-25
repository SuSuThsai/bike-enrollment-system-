package job

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"bike-enroll/server/internal/model"
)

// GroupReconcileResult 是单个组别的对账结果。
type GroupReconcileResult struct {
	GroupID        string
	TotalSlots     int
	RedisRemaining int
	RedisExists    bool
	ActiveLocks    int64
	PendingPayment int64
	PaidConfirmed  int64
	Difference     int64
	Consistent     bool
}

// ReconcileResult 是一次对账的汇总。
type ReconcileResult struct {
	Groups     []GroupReconcileResult
	Mismatches int
	Locked     bool
}

// ReconcileJob 校验 Redis 名额、活跃 lock、待支付与已支付报名是否等于总名额。
type ReconcileJob struct {
	db     *gorm.DB
	rdb    *redis.Client
	lock   *jobLock
	logger *slog.Logger
}

// NewReconcileJob 创建对账任务。
func NewReconcileJob(db *gorm.DB, rdb *redis.Client, logger *slog.Logger) *ReconcileJob {
	if logger == nil {
		logger = slog.Default()
	}
	return &ReconcileJob{db: db, rdb: rdb, lock: newJobLock(rdb, logger), logger: logger}
}

// Run 执行一次全量 group 对账；只读，不修改 Redis/MySQL。
func (j *ReconcileJob) Run(ctx context.Context) (ReconcileResult, error) {
	token, acquired, err := j.lock.acquire(ctx, reconcileJob)
	if err != nil {
		return ReconcileResult{}, fmt.Errorf("acquire reconcile job lock: %w", err)
	}
	if !acquired {
		j.logger.Info("reconcile job skipped: job lock held")
		return ReconcileResult{Locked: true}, nil
	}
	defer j.lock.release(reconcileJob, token)

	var groups []model.Group
	if err := j.db.WithContext(ctx).Order("id ASC").Find(&groups).Error; err != nil {
		return ReconcileResult{}, fmt.Errorf("load groups for reconcile: %w", err)
	}

	result := ReconcileResult{Groups: make([]GroupReconcileResult, 0, len(groups))}
	for _, group := range groups {
		item, err := j.reconcileGroup(ctx, group)
		if err != nil {
			return result, err
		}
		result.Groups = append(result.Groups, item)
		if !item.Consistent {
			result.Mismatches++
			j.logger.ErrorContext(ctx, "reconciliation mismatch",
				"alert", true,
				"code", "RECONCILIATION_MISMATCH",
				"group_id", item.GroupID,
				"redis_remaining", item.RedisRemaining,
				"redis_exists", item.RedisExists,
				"active_locks", item.ActiveLocks,
				"pending_payment", item.PendingPayment,
				"paid_confirmed", item.PaidConfirmed,
				"total_slots", item.TotalSlots,
				"difference", item.Difference,
			)
		}
	}
	return result, nil
}

func (j *ReconcileJob) reconcileGroup(ctx context.Context, group model.Group) (GroupReconcileResult, error) {
	item := GroupReconcileResult{
		GroupID:    group.ID,
		TotalSlots: group.TotalSlots,
	}

	redisRemaining, err := j.rdb.Get(ctx, "group:"+group.ID+":remaining").Int()
	if errors.Is(err, redis.Nil) {
		item.RedisExists = false
		item.RedisRemaining = 0
	} else if err != nil {
		return item, fmt.Errorf("read group %s remaining: %w", group.ID, err)
	} else {
		item.RedisExists = true
		item.RedisRemaining = redisRemaining
	}

	if err := j.db.WithContext(ctx).Model(&model.EnrollmentLock{}).
		Where("group_id = ? AND status = ?", group.ID, "active").
		Count(&item.ActiveLocks).Error; err != nil {
		return item, fmt.Errorf("count active locks for group %s: %w", group.ID, err)
	}
	if err := j.db.WithContext(ctx).Model(&model.Enrollment{}).
		Where("group_id = ? AND status = ?", group.ID, "pending_payment").
		Count(&item.PendingPayment).Error; err != nil {
		return item, fmt.Errorf("count pending enrollments for group %s: %w", group.ID, err)
	}
	if err := j.db.WithContext(ctx).Model(&model.Enrollment{}).
		Where("group_id = ? AND status IN ?", group.ID, []string{"paid", "confirmed"}).
		Count(&item.PaidConfirmed).Error; err != nil {
		return item, fmt.Errorf("count paid enrollments for group %s: %w", group.ID, err)
	}

	item.Difference = int64(item.RedisRemaining) + item.ActiveLocks + item.PendingPayment + item.PaidConfirmed - int64(item.TotalSlots)
	item.Consistent = item.RedisExists && item.Difference == 0
	return item, nil
}
