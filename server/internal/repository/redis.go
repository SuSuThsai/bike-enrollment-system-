// Package repository 提供基础设施连接与数据访问实现。
package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"bike-enroll/server/internal/config"
)

// OpenRedis 创建 Redis 客户端并做一次 Ping。
func OpenRedis(cfg config.RedisConfig) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return client, nil
}
