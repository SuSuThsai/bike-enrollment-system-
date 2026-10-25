//go:build integration

package admin_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"bike-enroll/server/internal/config"
	"bike-enroll/server/internal/model"
	"bike-enroll/server/internal/repository"
	adminrepo "bike-enroll/server/internal/repository/admin"
	adminsvc "bike-enroll/server/internal/service/admin"
)

func TestAdminEventGroupListAndStats(t *testing.T) {
	cfg := config.Load()
	db, err := repository.OpenMySQL(cfg.MySQL)
	if err != nil {
		t.Fatalf("连接 MySQL 失败: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("获取 sql.DB 失败: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	rdb, err := repository.OpenRedis(cfg.Redis)
	if err != nil {
		t.Fatalf("连接 Redis 失败: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })

	service := adminsvc.New(adminrepo.NewGORM(db, rdb), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	now := time.Now()

	eventDetail, err := service.CreateEvent(ctx, adminrepo.CreateEventParams{
		Title:                "后台创建赛事",
		RegistrationOpenAt:   now.Add(-24 * time.Hour),
		RegistrationDeadline: now.Add(12 * time.Hour),
		StartTime:            now.Add(24 * time.Hour),
		EndTime:              now.Add(26 * time.Hour),
		Location:             "上海",
		Description:          "desc", Rules: "", Now: now,
	})
	if err != nil {
		t.Fatalf("创建赛事失败: %v", err)
	}
	eventID := eventDetail.Event.ID
	t.Cleanup(func() {
		var groupIDs []string
		db.Model(&model.Group{}).Where("event_id = ?", eventID).Pluck("id", &groupIDs)
		db.Where("group_id IN ?", groupIDs).Delete(&model.EnrollmentLock{})
		db.Where("event_id = ?", eventID).Delete(&model.Enrollment{})
		db.Where("event_id = ?", eventID).Delete(&model.Group{})
		db.Where("id = ?", eventID).Delete(&model.Event{})
	})
	if eventDetail.Event.LifecycleStatus != model.LifecyclePublished || len(eventDetail.Groups) != 0 {
		t.Errorf("创建赛事状态/组别异常: %+v", eventDetail)
	}

	title := "后台更新赛事"
	updated, err := service.UpdateEvent(ctx, adminrepo.UpdateEventParams{
		EventID: eventID, Title: &title, Now: now,
	})
	if err != nil {
		t.Fatalf("更新赛事失败: %v", err)
	}
	if updated.Event.Title != title {
		t.Errorf("更新后标题 = %q，期望 %q", updated.Event.Title, title)
	}

	groupResult, err := service.CreateGroup(ctx, adminrepo.CreateGroupParams{
		EventID: eventID, Name: "后台组", Price: 1000, TotalSlots: 10,
		StartTime: now.Add(24 * time.Hour), FormSchema: map[string]any{},
		DisclaimerVersion: "v1", Now: now,
	})
	if err != nil {
		t.Fatalf("创建组别失败: %v", err)
	}
	groupID := groupResult.Group.ID
	redisKey := "group:" + groupID + ":remaining"
	t.Cleanup(func() { rdb.Del(context.Background(), redisKey) })
	if groupResult.RemainingSlots != 10 {
		t.Errorf("初始化 remaining = %d，期望 10", groupResult.RemainingSlots)
	}
	remaining, _ := rdb.Get(ctx, redisKey).Int()
	if remaining != 10 {
		t.Errorf("Redis remaining = %d，期望 10", remaining)
	}

	lock := &model.EnrollmentLock{
		Token: uuid.NewString(), UserID: uuid.NewString(), GroupID: groupID,
		Status: "active", ExpiresAt: now.Add(15 * time.Minute), CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(lock).Error; err != nil {
		t.Fatalf("创建 lock 失败: %v", err)
	}
	for _, status := range []string{"pending_payment", "paid"} {
		enrollment := &model.Enrollment{
			ID: uuid.NewString(), UserID: uuid.NewString(), EventID: eventID,
			GroupID: groupID, OrderID: uuid.NewString(), Status: status,
			FormData:  map[string]any{"id_card": "123456", "name": status},
			CreatedAt: now, UpdatedAt: now,
		}
		if err := db.Create(enrollment).Error; err != nil {
			t.Fatalf("创建 enrollment 失败: %v", err)
		}
	}
	if err := rdb.Set(ctx, redisKey, 7, 0).Err(); err != nil {
		t.Fatalf("设置 Redis 失败: %v", err)
	}

	page, err := service.ListEnrollments(ctx, adminrepo.EnrollmentFilter{
		EventID: eventID, Page: 1, PageSize: 1,
	})
	if err != nil {
		t.Fatalf("查询名单失败: %v", err)
	}
	if page.Total != 2 || len(page.Items) != 1 {
		t.Errorf("分页结果异常: total=%d items=%d", page.Total, len(page.Items))
	}
	if page.Items[0].FormData["id_card"] != "***" {
		t.Errorf("身份证字段未脱敏: %+v", page.Items[0].FormData)
	}

	stats, err := service.StatsOverview(ctx, eventID)
	if err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if len(stats.Groups) != 1 {
		t.Fatalf("统计组别数 = %d，期望 1", len(stats.Groups))
	}
	groupStats := stats.Groups[0]
	if groupStats.TotalSlots != 10 || groupStats.ActiveLockCount != 1 ||
		groupStats.PendingPaymentCount != 1 || groupStats.PaidCount != 1 ||
		groupStats.RemainingSlots != 7 {
		t.Errorf("统计口径错误: %+v", groupStats)
	}
	if groupStats.RemainingSlots+int(groupStats.ActiveLockCount)+
		int(groupStats.PendingPaymentCount)+int(groupStats.PaidCount) != groupStats.TotalSlots {
		t.Errorf("统计不满足对账等式: %+v", groupStats)
	}
}

func TestExportEnrollmentsIntegration(t *testing.T) {
	cfg := config.Load()
	db, err := repository.OpenMySQL(cfg.MySQL)
	if err != nil {
		t.Fatalf("连接 MySQL 失败: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("获取 sql.DB 失败: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	rdb, err := repository.OpenRedis(cfg.Redis)
	if err != nil {
		t.Fatalf("连接 Redis 失败: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })

	now := time.Now()
	eventID := uuid.NewString()
	event := &model.Event{
		ID: eventID, Title: "导出测试赛事",
		StartTime: now.Add(24 * time.Hour), EndTime: now.Add(26 * time.Hour),
		Location: "上海", LifecycleStatus: model.LifecyclePublished, Description: "desc", Rules: "",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(event).Error; err != nil {
		t.Fatalf("创建 event 失败: %v", err)
	}
	for i := 0; i < 2; i++ {
		enrollment := &model.Enrollment{
			ID: uuid.NewString(), UserID: uuid.NewString(), EventID: eventID,
			GroupID: uuid.NewString(), OrderID: uuid.NewString(), Status: "paid",
			FormData: map[string]any{"name": "test"}, CreatedAt: now, UpdatedAt: now,
		}
		if err := db.Create(enrollment).Error; err != nil {
			t.Fatalf("创建 enrollment 失败: %v", err)
		}
	}
	t.Cleanup(func() {
		db.Where("event_id = ?", eventID).Delete(&model.Enrollment{})
		db.Where("id = ?", eventID).Delete(&model.Event{})
	})

	service := adminsvc.New(adminrepo.NewGORM(db, rdb), slog.New(slog.NewTextHandler(io.Discard, nil)))
	result, err := service.Export(context.Background(), "admin-1", adminrepo.ExportFilter{EventID: eventID})
	if err != nil {
		t.Fatalf("Export 失败: %v", err)
	}
	if result.Rows != 2 || result.Total != 2 || result.Truncated {
		t.Errorf("导出结果异常: rows=%d total=%d truncated=%v", result.Rows, result.Total, result.Truncated)
	}
	if len(result.Data) < 2 || result.Data[0] != 'P' || result.Data[1] != 'K' {
		t.Errorf("导出内容不是 xlsx zip: len=%d", len(result.Data))
	}
}

func TestUpdateGroupSyncsRedisWithDelta(t *testing.T) {
	cfg := config.Load()
	db, err := repository.OpenMySQL(cfg.MySQL)
	if err != nil {
		t.Fatalf("连接 MySQL 失败: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("获取 sql.DB 失败: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	rdb, err := repository.OpenRedis(cfg.Redis)
	if err != nil {
		t.Fatalf("连接 Redis 失败: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })

	now := time.Now()
	groupID := uuid.NewString()
	group := &model.Group{
		ID: groupID, EventID: uuid.NewString(), Name: "调整测试组",
		Price: 1000, TotalSlots: 100, StartTime: now.Add(24 * time.Hour),
		Status: "open", FormSchema: map[string]any{}, DisclaimerVersion: "v1",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(group).Error; err != nil {
		t.Fatalf("创建 group 失败: %v", err)
	}
	key := "group:" + groupID + ":remaining"
	if err := rdb.Set(context.Background(), key, 10, 0).Err(); err != nil {
		t.Fatalf("初始化 Redis 失败: %v", err)
	}
	t.Cleanup(func() {
		db.Where("id = ?", groupID).Delete(&model.Group{})
		rdb.Del(context.Background(), key)
	})

	repo := adminrepo.NewGORM(db, rdb)
	service := adminsvc.New(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()

	total := 90
	result, err := service.UpdateGroup(ctx, adminrepo.UpdateGroupParams{
		GroupID:    groupID,
		TotalSlots: &total,
	})
	if err != nil {
		t.Fatalf("调整名额失败: %v", err)
	}
	if result.RemainingSlots != 0 {
		t.Errorf("remaining = %d，期望 0", result.RemainingSlots)
	}
	remaining, _ := rdb.Get(ctx, key).Int()
	if remaining != 0 {
		t.Errorf("Redis remaining = %d，期望 0", remaining)
	}
	var saved model.Group
	db.Where("id = ?", groupID).First(&saved)
	if saved.TotalSlots != 90 {
		t.Errorf("MySQL total_slots = %d，期望 90", saved.TotalSlots)
	}

	total = 80
	_, err = service.UpdateGroup(ctx, adminrepo.UpdateGroupParams{
		GroupID:    groupID,
		TotalSlots: &total,
	})
	if !errors.Is(err, adminrepo.ErrGroupSoldOut) {
		t.Fatalf("err = %v，期望 ErrGroupSoldOut", err)
	}
	remaining, _ = rdb.Get(ctx, key).Int()
	if remaining != 0 {
		t.Errorf("拒绝后 Redis remaining = %d，期望仍为 0", remaining)
	}
	db.Where("id = ?", groupID).First(&saved)
	if saved.TotalSlots != 90 {
		t.Errorf("拒绝后 MySQL total_slots = %d，期望仍为 90", saved.TotalSlots)
	}
}

// TestRebuildAndDeleteGroupIntegration 覆盖名额重建与赛事/组别删除的完整流程。
func TestRebuildAndDeleteGroupIntegration(t *testing.T) {
	cfg := config.Load()
	db, err := repository.OpenMySQL(cfg.MySQL)
	if err != nil {
		t.Fatalf("连接 MySQL 失败: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("获取 sql.DB 失败: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	rdb, err := repository.OpenRedis(cfg.Redis)
	if err != nil {
		t.Fatalf("连接 Redis 失败: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })

	service := adminsvc.New(adminrepo.NewGORM(db, rdb), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	now := time.Now()

	eventDetail, err := service.CreateEvent(ctx, adminrepo.CreateEventParams{
		Title:                "重建与清理测试赛事",
		RegistrationOpenAt:   now.Add(-24 * time.Hour),
		RegistrationDeadline: now.Add(12 * time.Hour),
		StartTime:            now.Add(24 * time.Hour),
		EndTime:              now.Add(26 * time.Hour),
		Location:             "上海",
		Description:          "desc", Now: now,
	})
	if err != nil {
		t.Fatalf("创建赛事失败: %v", err)
	}
	eventID := eventDetail.Event.ID

	groupResult, err := service.CreateGroup(ctx, adminrepo.CreateGroupParams{
		EventID: eventID, Name: "重建测试组", Price: 100, TotalSlots: 5,
		StartTime: now.Add(24 * time.Hour), FormSchema: map[string]any{},
		DisclaimerVersion: "v1", Now: now,
	})
	if err != nil {
		t.Fatalf("创建组别失败: %v", err)
	}
	groupID := groupResult.Group.ID
	redisKey := "group:" + groupID + ":remaining"

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		rdb.Del(cleanupCtx, redisKey)
		db.Where("group_id = ?", groupID).Delete(&model.EnrollmentLock{})
		db.Where("group_id = ?", groupID).Delete(&model.Enrollment{})
		db.Where("group_id = ?", groupID).Delete(&model.Order{})
		db.Where("id = ?", groupID).Delete(&model.Group{})
		db.Where("id = ?", eventID).Delete(&model.Event{})
	})

	// 1) key 缺失时按对账等式重建。
	if err := rdb.Del(ctx, redisKey).Err(); err != nil {
		t.Fatalf("删除 Redis key 失败: %v", err)
	}
	rebuilt, err := service.RebuildGroupRemaining(ctx, "admin-integration", groupID, false)
	if err != nil {
		t.Fatalf("重建名额失败: %v", err)
	}
	if rebuilt.Mode != "created" || rebuilt.TotalSlots != 5 || rebuilt.Expected != 5 {
		t.Errorf("重建结果异常: %+v", rebuilt)
	}
	if rebuilt.HasPrevious {
		t.Errorf("key 原本缺失时不应有 previous: %+v", rebuilt)
	}
	if remaining, _ := rdb.Get(ctx, redisKey).Int(); remaining != 5 {
		t.Errorf("重建后 Redis remaining = %d，期望 5", remaining)
	}

	// 2) 幂等：再次重建返回 unchanged。
	again, err := service.RebuildGroupRemaining(ctx, "admin-integration", groupID, false)
	if err != nil {
		t.Fatalf("重复重建失败: %v", err)
	}
	if again.Mode != "unchanged" {
		t.Errorf("重复重建 mode = %q，期望 unchanged", again.Mode)
	}

	// 3) 漂移但未开 force：返回 conflict，且不改写 Redis。
	if err := rdb.Set(ctx, redisKey, 99, 0).Err(); err != nil {
		t.Fatalf("写入漂移值失败: %v", err)
	}
	conflict, err := service.RebuildGroupRemaining(ctx, "admin-integration", groupID, false)
	if err != nil {
		t.Fatalf("冲突场景返回错误: %v", err)
	}
	if conflict.Mode != "conflict" || !conflict.HasPrevious || conflict.Previous != 99 {
		t.Errorf("冲突结果异常: %+v", conflict)
	}
	if remaining, _ := rdb.Get(ctx, redisKey).Int(); remaining != 99 {
		t.Errorf("冲突时不应改写 Redis，实际 %d", remaining)
	}

	// 4) force=true 覆盖漂移值。
	forced, err := service.RebuildGroupRemaining(ctx, "admin-integration", groupID, true)
	if err != nil {
		t.Fatalf("force 重建失败: %v", err)
	}
	if forced.Mode != "overwritten" || forced.Previous != 99 {
		t.Errorf("force 重建结果异常: %+v", forced)
	}
	if remaining, _ := rdb.Get(ctx, redisKey).Int(); remaining != 5 {
		t.Errorf("force 重建后 Redis remaining = %d，期望 5", remaining)
	}

	// 5) 赛事下仍有组别：禁止删除赛事。
	if _, err := service.DeleteEvent(ctx, "admin-integration", eventID); !errors.Is(err, adminrepo.ErrEventHasGroups) {
		t.Fatalf("删除仍有组别的赛事: err = %v，期望 ErrEventHasGroups", err)
	}

	// 6) 组别存在锁：禁止删除组别。
	lock := &model.EnrollmentLock{
		Token: uuid.NewString(), UserID: uuid.NewString(), GroupID: groupID,
		Status: "active", ExpiresAt: now.Add(15 * time.Minute), CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(lock).Error; err != nil {
		t.Fatalf("创建 lock 失败: %v", err)
	}
	if _, err := service.DeleteGroup(ctx, "admin-integration", groupID); !errors.Is(err, adminrepo.ErrGroupInUse) {
		t.Fatalf("删除有锁的组别: err = %v，期望 ErrGroupInUse", err)
	}

	// 7) 清掉锁后可以删除组别，并同步清理 Redis key。
	if err := db.Where("token = ?", lock.Token).Delete(&model.EnrollmentLock{}).Error; err != nil {
		t.Fatalf("删除 lock 失败: %v", err)
	}
	deleted, err := service.DeleteGroup(ctx, "admin-integration", groupID)
	if err != nil {
		t.Fatalf("删除组别失败: %v", err)
	}
	if !deleted.RedisDeleted || deleted.RedisError != "" {
		t.Errorf("Redis key 未清理: %+v", deleted)
	}
	if exists, _ := rdb.Exists(ctx, redisKey).Result(); exists != 0 {
		t.Errorf("删除组别后 Redis key 仍存在")
	}

	// 8) 删除赛事成功；重复删除返回 ErrEventNotFound。
	if _, err := service.DeleteEvent(ctx, "admin-integration", eventID); err != nil {
		t.Fatalf("删除赛事失败: %v", err)
	}
	if _, err := service.DeleteEvent(ctx, "admin-integration", eventID); !errors.Is(err, adminrepo.ErrEventNotFound) {
		t.Fatalf("重复删除赛事: err = %v，期望 ErrEventNotFound", err)
	}
}
