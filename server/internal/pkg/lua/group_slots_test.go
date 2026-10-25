package lua_test

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"bike-enroll/server/internal/pkg/lua"
)

func TestGroupSlotsScriptAppliesDeltaAtomically(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	key := "group:g1:remaining"
	if err := client.Set(ctx, key, 10, 0).Err(); err != nil {
		t.Fatalf("初始化 Redis 失败: %v", err)
	}
	script := redis.NewScript(lua.GroupSlotsScript)

	value, err := script.Run(ctx, client, []string{key}, 5).Int64()
	if err != nil || value != 15 {
		t.Fatalf("delta +5: value=%d err=%v，期望 15", value, err)
	}
	value, err = script.Run(ctx, client, []string{key}, -3).Int64()
	if err != nil || value != 12 {
		t.Fatalf("delta -3: value=%d err=%v，期望 12", value, err)
	}

	value, err = script.Run(ctx, client, []string{key}, -13).Int64()
	if err != nil || value != -1 {
		t.Fatalf("负剩余: value=%d err=%v，期望 -1", value, err)
	}
	stored, _ := client.Get(ctx, key).Int()
	if stored != 12 {
		t.Errorf("拒绝负剩余后 Redis = %d，期望仍为 12", stored)
	}

	value, err = script.Run(ctx, client, []string{"group:missing:remaining"}, 1).Int64()
	if err != nil || value != -2 {
		t.Fatalf("key 缺失: value=%d err=%v，期望 -2", value, err)
	}
}
