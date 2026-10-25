package job

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"bike-enroll/server/internal/model"
)

// LockExpiryResult 是 lock 过期任务执行结果。
type LockExpiryResult struct {
	Reclaimed int
	Locked    bool
}

// LockExpiryJob 负责将过期 active lock 置为 expired 并回补 Redis。
type LockExpiryJob struct {
	db     *gorm.DB
	rdb    *redis.Client
	lock   *jobLock
	logger *slog.Logger
}

// NewLockExpiryJob 创建 lock 过期任务。
func NewLockExpiryJob(db *gorm.DB, rdb *redis.Client, logger *slog.Logger) *LockExpiryJob {
	if logger == nil {
		logger = slog.Default()
	}
	return &LockExpiryJob{db: db, rdb: rdb, lock: newJobLock(rdb, logger), logger: logger}
}

// Run 执行一次 lock 过期扫描。
func (j *LockExpiryJob) Run(ctx context.Context) (LockExpiryResult, error) {
	token, acquired, err := j.lock.acquire(ctx, lockExpiryJob)
	if err != nil {
		return LockExpiryResult{}, fmt.Errorf("acquire lock-expiry job lock: %w", err)
	}
	if !acquired {
		j.logger.Info("lock-expiry job skipped: job lock held")
		return LockExpiryResult{Locked: true}, nil
	}
	defer j.lock.release(lockExpiryJob, token)

	var rows []struct {
		Token   string `gorm:"column:token"`
		GroupID string `gorm:"column:group_id"`
	}
	if err := j.db.WithContext(ctx).Raw(`
		SELECT token, group_id
		  FROM enrollment_locks
		 WHERE status = 'active' AND expires_at < NOW(3)
		 ORDER BY expires_at
		 LIMIT 200
		 FOR UPDATE SKIP LOCKED`).Scan(&rows).Error; err != nil {
		return LockExpiryResult{}, fmt.Errorf("scan expired locks: %w", err)
	}

	result := LockExpiryResult{}
	now := time.Now()
	for _, row := range rows {
		applied := false
		err := j.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			update := tx.Model(&model.EnrollmentLock{}).
				Where("token = ? AND status = ?", row.Token, "active").
				Updates(map[string]any{
					"status":     "expired",
					"expired_at": now,
					"updated_at": now,
				})
			if update.Error != nil {
				return fmt.Errorf("expire lock %s: %w", row.Token, update.Error)
			}
			if update.RowsAffected != 1 {
				return nil
			}
			if err := j.rdb.Incr(ctx, "group:"+row.GroupID+":remaining").Err(); err != nil {
				return fmt.Errorf("reclaim group %s: %w", row.GroupID, err)
			}
			applied = true
			return nil
		})
		if err != nil {
			return result, err
		}
		if applied {
			result.Reclaimed++
		}
	}
	if result.Reclaimed > 0 {
		j.logger.Info("lock-expiry job finished", "reclaimed", result.Reclaimed)
	}
	return result, nil
}
