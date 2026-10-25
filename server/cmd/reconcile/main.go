// Command reconcile 手动执行一次全量名额对账。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"bike-enroll/server/internal/config"
	"bike-enroll/server/internal/job"
	"bike-enroll/server/internal/repository"
)

func main() {
	os.Exit(run())
}

func run() int {
	cfg := config.Load()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	db, err := repository.OpenMySQL(cfg.MySQL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open mysql: %v\n", err)
		return 1
	}
	sqlDB, err := db.DB()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get sql.DB: %v\n", err)
		return 1
	}
	defer sqlDB.Close()

	rdb, err := repository.OpenRedis(cfg.Redis)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open redis: %v\n", err)
		return 1
	}
	defer rdb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reconciler := job.NewReconcileJob(db, rdb, logger)
	result, err := reconciler.Run(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reconcile failed: %v\n", err)
		return 1
	}
	if result.Locked {
		fmt.Fprintln(os.Stderr, "reconcile skipped: job lock held")
		return 1
	}

	for _, group := range result.Groups {
		status := "OK"
		if !group.Consistent {
			status = "MISMATCH"
		}
		fmt.Printf(
			"group=%s status=%s total=%d redis=%d redis_exists=%t active_locks=%d pending_payment=%d paid_confirmed=%d difference=%d\n",
			group.GroupID,
			status,
			group.TotalSlots,
			group.RedisRemaining,
			group.RedisExists,
			group.ActiveLocks,
			group.PendingPayment,
			group.PaidConfirmed,
			group.Difference,
		)
	}

	if len(result.Groups) == 0 {
		fmt.Println("no groups to reconcile")
	}
	if result.Mismatches > 0 {
		fmt.Fprintf(os.Stderr, "reconcile finished with %d mismatch(es)\n", result.Mismatches)
		return 1
	}
	fmt.Printf("reconcile ok: %d group(s)\n", len(result.Groups))
	return 0
}
