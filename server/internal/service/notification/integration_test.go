//go:build integration

package notification_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"bike-enroll/server/internal/config"
	"bike-enroll/server/internal/model"
	"bike-enroll/server/internal/repository"
	notificationrepo "bike-enroll/server/internal/repository/notification"
	notificationsvc "bike-enroll/server/internal/service/notification"
)

func TestGetEnrollmentNotificationJoin(t *testing.T) {
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

	now := time.Now()
	userID := uuid.NewString()
	eventID := uuid.NewString()
	groupID := uuid.NewString()
	enrollmentID := uuid.NewString()
	orderID := strings.ReplaceAll(uuid.NewString(), "-", "")

	user := &model.User{
		ID: userID, OpenID: "openid-" + userID, Role: "rider",
		CreatedAt: now, UpdatedAt: now,
	}
	event := &model.Event{
		ID: eventID, Title: "通知测试赛事",
		StartTime: now.Add(24 * time.Hour), EndTime: now.Add(26 * time.Hour),
		Location: "测试地点", LifecycleStatus: model.LifecyclePublished, Description: "desc", Rules: "",
		CreatedAt: now, UpdatedAt: now,
	}
	group := &model.Group{
		ID: groupID, EventID: eventID, Name: "通知测试组", Price: 1000, TotalSlots: 10,
		StartTime: now.Add(24 * time.Hour), Status: "open",
		FormSchema: map[string]any{}, DisclaimerVersion: "v1",
		CreatedAt: now, UpdatedAt: now,
	}
	enrollment := &model.Enrollment{
		ID: enrollmentID, UserID: userID, EventID: eventID, GroupID: groupID, OrderID: orderID,
		Status: "paid", FormData: map[string]any{}, PaidAt: &now,
		CreatedAt: now, UpdatedAt: now,
	}
	order := &model.Order{
		ID: orderID, EnrollmentID: enrollmentID, UserID: userID, GroupID: groupID,
		Amount: 1000, Status: "paid", PaidAt: &now, CreatedAt: now, UpdatedAt: now,
	}
	for _, value := range []any{user, event, group, enrollment, order} {
		if err := db.Create(value).Error; err != nil {
			t.Fatalf("创建通知测试数据失败: %v", err)
		}
	}
	t.Cleanup(func() {
		db.Where("id = ?", orderID).Delete(&model.Order{})
		db.Where("id = ?", enrollmentID).Delete(&model.Enrollment{})
		db.Where("id = ?", groupID).Delete(&model.Group{})
		db.Where("id = ?", eventID).Delete(&model.Event{})
		db.Where("id = ?", userID).Delete(&model.User{})
	})

	item, err := notificationrepo.NewGORM(db).GetEnrollmentNotification(context.Background(), orderID)
	if err != nil {
		t.Fatalf("GetEnrollmentNotification 失败: %v", err)
	}
	if item.UserID != userID || item.OpenID != user.OpenID || item.EventTitle != event.Title ||
		item.GroupName != group.Name || item.OrderID != orderID || item.Amount != order.Amount {
		t.Errorf("通知数据错误: %+v", item)
	}
	if item.PaidAt == nil {
		t.Error("PaidAt 不应为空")
	}
}

func TestSubscribeAndConsumeAuthorization(t *testing.T) {
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

	userID := uuid.NewString()
	repo := notificationrepo.NewGORM(db)
	service := notificationsvc.New(repo, nil, "tpl-enroll", nil)
	defer service.Close(context.Background())
	t.Cleanup(func() {
		db.Where("user_id = ?", userID).Delete(&model.NotificationAuthorization{})
	})

	ctx := context.Background()
	if err := service.Subscribe(ctx, userID, []string{"tpl-enroll", "tpl-enroll", "tpl-other"}); err != nil {
		t.Fatalf("Subscribe 失败: %v", err)
	}

	var available int64
	if err := db.Model(&model.NotificationAuthorization{}).
		Where("user_id = ? AND status = ?", userID, "available").
		Count(&available).Error; err != nil {
		t.Fatalf("统计授权失败: %v", err)
	}
	if available != 2 {
		t.Errorf("available 授权数 = %d，期望 2（去重后两个模板）", available)
	}

	authorized, err := repo.ConsumeAuthorization(ctx, userID, "tpl-enroll")
	if err != nil || !authorized {
		t.Fatalf("首次消费失败: authorized=%v err=%v", authorized, err)
	}
	authorized, err = repo.ConsumeAuthorization(ctx, userID, "tpl-enroll")
	if err != nil {
		t.Fatalf("第二次消费出错: %v", err)
	}
	if authorized {
		t.Error("同一模板授权已被消费后不应再次成功")
	}

	var consumed model.NotificationAuthorization
	if err := db.Where("user_id = ? AND template_id = ?", userID, "tpl-enroll").First(&consumed).Error; err != nil {
		t.Fatalf("读取授权记录失败: %v", err)
	}
	if consumed.Status != "consumed" || consumed.ConsumedAt == nil {
		t.Errorf("授权记录状态错误: %+v", consumed)
	}
}
