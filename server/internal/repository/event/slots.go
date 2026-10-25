package event

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// RedisSlotRepository 从 Redis 读取组别实时剩余名额。
type RedisSlotRepository struct {
	client *redis.Client
}

// NewRedisSlots 创建 Redis 名额读取仓储。
func NewRedisSlots(client *redis.Client) *RedisSlotRepository {
	return &RedisSlotRepository{client: client}
}

// GetRemaining 读取 group:{group_id}:remaining。
//
// key 不存在时返回 exists=false，由 service 回退到 MySQL total_slots。
func (r *RedisSlotRepository) GetRemaining(ctx context.Context, groupID string) (int, bool, error) {
	value, err := r.client.Get(ctx, remainingKey(groupID)).Int()
	if errors.Is(err, redis.Nil) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read group remaining: %w", err)
	}
	return value, true, nil
}

func remainingKey(groupID string) string {
	return "group:" + groupID + ":remaining"
}
