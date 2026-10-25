// Package job 实现定时清理任务。
package job

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"bike-enroll/server/internal/pkg/lua"
)

const (
	jobLockTTL    = 60 * time.Second
	lockExpiryJob = "lock-expiry"
	payTimeoutJob = "pay-timeout"
	reconcileJob  = "reconcile"
)

type jobLock struct {
	rdb    *redis.Client
	script *redis.Script
	logger *slog.Logger
}

func newJobLock(rdb *redis.Client, logger *slog.Logger) *jobLock {
	if logger == nil {
		logger = slog.Default()
	}
	return &jobLock{
		rdb:    rdb,
		script: redis.NewScript(lua.JobUnlockScript),
		logger: logger,
	}
}

func (l *jobLock) acquire(ctx context.Context, name string) (string, bool, error) {
	token := uuid.NewString()
	ok, err := l.rdb.SetNX(ctx, "job:"+name+":lock", token, jobLockTTL).Result()
	if err != nil {
		return "", false, err
	}
	return token, ok, nil
}

func (l *jobLock) release(name, token string) {
	if token == "" {
		return
	}
	releaseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := l.script.Run(releaseCtx, l.rdb, []string{"job:" + name + ":lock"}, token).Err(); err != nil {
		l.logger.Warn("release job lock failed", "job", name, "error", err)
	}
}
