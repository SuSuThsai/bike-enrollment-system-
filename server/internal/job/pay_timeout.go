package job

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"bike-enroll/server/internal/model"
)

// PayTimeoutResult 是支付超时任务执行结果。
type PayTimeoutResult struct {
	Expired int
	Locked  bool
}

// PayTimeoutJob 负责将超时未支付订单置为 expired，并取消报名、回补 Redis。
type PayTimeoutJob struct {
	db      *gorm.DB
	rdb     *redis.Client
	lock    *jobLock
	timeout time.Duration
	logger  *slog.Logger
}

// NewPayTimeoutJob 创建支付超时任务。
func NewPayTimeoutJob(db *gorm.DB, rdb *redis.Client, timeout time.Duration, logger *slog.Logger) *PayTimeoutJob {
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &PayTimeoutJob{
		db:      db,
		rdb:     rdb,
		lock:    newJobLock(rdb, logger),
		timeout: timeout,
		logger:  logger,
	}
}

// Run 执行一次支付超时扫描。
func (j *PayTimeoutJob) Run(ctx context.Context) (PayTimeoutResult, error) {
	token, acquired, err := j.lock.acquire(ctx, payTimeoutJob)
	if err != nil {
		return PayTimeoutResult{}, fmt.Errorf("acquire pay-timeout job lock: %w", err)
	}
	if !acquired {
		j.logger.Info("pay-timeout job skipped: job lock held")
		return PayTimeoutResult{Locked: true}, nil
	}
	defer j.lock.release(payTimeoutJob, token)

	now := time.Now()
	var rows []struct {
		OrderID string `gorm:"column:order_id"`
		GroupID string `gorm:"column:group_id"`
	}
	if err := j.db.WithContext(ctx).Raw(`
		SELECT o.id AS order_id, e.group_id AS group_id
		  FROM orders o
		  JOIN enrollments e ON e.order_id = o.id
		 WHERE o.status = 'pending_payment' AND o.created_at < ?
		 ORDER BY o.created_at
		 LIMIT 200
		 FOR UPDATE SKIP LOCKED`,
		now.Add(-j.timeout),
	).Scan(&rows).Error; err != nil {
		return PayTimeoutResult{}, fmt.Errorf("scan timeout orders: %w", err)
	}

	result := PayTimeoutResult{}
	for _, row := range rows {
		expired := false
		err := j.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			orderUpdate := tx.Model(&model.Order{}).
				Where("id = ? AND status = ?", row.OrderID, "pending_payment").
				Updates(map[string]any{
					"status":     "expired",
					"expired_at": now,
					"updated_at": now,
				})
			if orderUpdate.Error != nil {
				return fmt.Errorf("expire order %s: %w", row.OrderID, orderUpdate.Error)
			}
			if orderUpdate.RowsAffected != 1 {
				return nil
			}

			enrollmentUpdate := tx.Model(&model.Enrollment{}).
				Where("order_id = ? AND status = ?", row.OrderID, "pending_payment").
				Updates(map[string]any{
					"status":       "cancelled",
					"cancelled_at": now,
					"updated_at":   now,
				})
			if enrollmentUpdate.Error != nil {
				return fmt.Errorf("cancel enrollment for order %s: %w", row.OrderID, enrollmentUpdate.Error)
			}
			if enrollmentUpdate.RowsAffected != 1 {
				return errors.New("pending enrollment not found for timeout order")
			}

			if err := j.rdb.Incr(ctx, "group:"+row.GroupID+":remaining").Err(); err != nil {
				return fmt.Errorf("reclaim group %s: %w", row.GroupID, err)
			}
			expired = true
			return nil
		})
		if err != nil {
			return result, err
		}
		if expired {
			result.Expired++
		}
	}
	if result.Expired > 0 {
		j.logger.Info("pay-timeout job finished", "expired", result.Expired)
	}
	return result, nil
}
