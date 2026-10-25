package lock

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"bike-enroll/server/internal/pkg/lua"
)

// ErrSoldOut 表示 Redis 名额已耗尽。
var ErrSoldOut = errors.New("group sold out")

const (
	userLockTTL      = 15 * time.Second
	rollbackGuardTTL = 24 * time.Hour
)

// SlotStore 是名额锁定所需的 Redis 操作。
type SlotStore interface {
	AcquireUserLock(ctx context.Context, userID, groupID string) (bool, error)
	Reserve(ctx context.Context, groupID string) (remaining int, err error)
	Rollback(ctx context.Context, groupID, lockToken string) error
	SetLockTokenTTL(ctx context.Context, token string, ttl time.Duration) error
}

// RedisStore 是 SlotStore 的 Redis 实现。
type RedisStore struct {
	client         *redis.Client
	lockScript     *redis.Script
	rollbackScript *redis.Script
}

// NewRedisStore 创建 Redis 名额操作仓储。
func NewRedisStore(client *redis.Client) *RedisStore {
	return &RedisStore{
		client:         client,
		lockScript:     redis.NewScript(lua.LockSlotScript),
		rollbackScript: redis.NewScript(lua.RollbackSlotScript),
	}
}

// AcquireUserLock 使用 SET NX PX 15000 串行化同一用户对同一组别的锁定。
func (r *RedisStore) AcquireUserLock(ctx context.Context, userID, groupID string) (bool, error) {
	ok, err := r.client.SetNX(ctx, userLockKey(userID, groupID), "1", userLockTTL).Result()
	if err != nil {
		return false, fmt.Errorf("acquire user lock: %w", err)
	}
	return ok, nil
}

// Reserve 通过 Lua 脚本原子执行“remaining > 0 判断 + DECR”。
func (r *RedisStore) Reserve(ctx context.Context, groupID string) (int, error) {
	remaining, err := r.lockScript.Run(ctx, r.client, []string{remainingKey(groupID)}, 1).Int64()
	if err != nil {
		return 0, fmt.Errorf("reserve group slot: %w", err)
	}
	if remaining < 0 {
		return 0, ErrSoldOut
	}
	return int(remaining), nil
}

// Rollback 在 MySQL 落库失败时回补 1 个名额。
//
// 以 lock_token 为补偿幂等键：同一 token 重复调用只会 INCR 一次。
func (r *RedisStore) Rollback(ctx context.Context, groupID, lockToken string) error {
	guardTTLMillis := int64(rollbackGuardTTL / time.Millisecond)
	if _, err := r.rollbackScript.Run(
		ctx,
		r.client,
		[]string{remainingKey(groupID), rollbackGuardKey(lockToken)},
		guardTTLMillis,
	).Int64(); err != nil {
		return fmt.Errorf("rollback group slot: %w", err)
	}
	return nil
}

// SetLockTokenTTL 写入辅助 TTL，权威状态仍以 MySQL 为准。
func (r *RedisStore) SetLockTokenTTL(ctx context.Context, token string, ttl time.Duration) error {
	if err := r.client.Set(ctx, lockTokenKey(token), "1", ttl).Err(); err != nil {
		return fmt.Errorf("set lock token ttl: %w", err)
	}
	return nil
}

func remainingKey(groupID string) string {
	return "group:" + groupID + ":remaining"
}

func userLockKey(userID, groupID string) string {
	return "lock:" + userID + ":" + groupID
}

func lockTokenKey(token string) string {
	return "lock:" + token
}

func rollbackGuardKey(token string) string {
	return "lock:rollback:" + token
}
