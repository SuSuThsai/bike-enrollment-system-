package job

import (
	"context"
	"log/slog"
	"time"
)

const (
	// lockExpiryInterval 是 lock 过期任务的执行间隔。
	//
	// 15 秒一次，保证超过 LOCK_TTL 的锁尽快回补；
	// 单实例锁 TTL 为 60s（见 lock.go 的 jobLockTTL），大于该间隔，
	// 即使实例异常退出，最多阻塞 4 个周期。
	lockExpiryInterval = 15 * time.Second
	// payTimeoutInterval 是支付超时任务的执行间隔，保持每分钟。
	payTimeoutInterval = time.Minute
	reconcileInterval  = 5 * time.Minute
)

// Scheduler 每 15 秒执行 lock 过期任务、每分钟执行支付超时任务、每 5 分钟执行对账。
type Scheduler struct {
	lockExpiry *LockExpiryJob
	payTimeout *PayTimeoutJob
	reconcile  *ReconcileJob
	logger     *slog.Logger
}

// NewScheduler 创建定时任务调度器。
func NewScheduler(lockExpiry *LockExpiryJob, payTimeout *PayTimeoutJob, reconcile *ReconcileJob, logger *slog.Logger) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{
		lockExpiry: lockExpiry,
		payTimeout: payTimeout,
		reconcile:  reconcile,
		logger:     logger,
	}
}

// Run 立即执行一轮，之后按各自频率执行，直到 ctx 取消。
func (s *Scheduler) Run(ctx context.Context) {
	s.runLockExpiry(ctx)
	s.runPayTimeout(ctx)
	s.runReconcile(ctx)

	lockTicker := time.NewTicker(lockExpiryInterval)
	payTicker := time.NewTicker(payTimeoutInterval)
	reconcileTicker := time.NewTicker(reconcileInterval)
	defer func() {
		lockTicker.Stop()
		payTicker.Stop()
		reconcileTicker.Stop()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-lockTicker.C:
			s.runLockExpiry(ctx)
		case <-payTicker.C:
			s.runPayTimeout(ctx)
		case <-reconcileTicker.C:
			s.runReconcile(ctx)
		}
	}
}

func (s *Scheduler) runLockExpiry(ctx context.Context) {
	if result, err := s.lockExpiry.Run(ctx); err != nil {
		s.logger.Error("lock-expiry job failed", "error", err)
	} else if result.Locked {
		s.logger.Info("lock-expiry job skipped: another instance running")
	}
}

func (s *Scheduler) runPayTimeout(ctx context.Context) {
	if result, err := s.payTimeout.Run(ctx); err != nil {
		s.logger.Error("pay-timeout job failed", "error", err)
	} else if result.Locked {
		s.logger.Info("pay-timeout job skipped: another instance running")
	}
}

func (s *Scheduler) runReconcile(ctx context.Context) {
	result, err := s.reconcile.Run(ctx)
	if err != nil {
		s.logger.Error("reconcile job failed", "error", err)
		return
	}
	if result.Locked {
		s.logger.Info("reconcile job skipped: job lock held")
		return
	}
	if result.Mismatches > 0 {
		s.logger.Error("reconcile finished with mismatches",
			"alert", true,
			"mismatches", result.Mismatches,
			"groups", len(result.Groups),
		)
	}
}
