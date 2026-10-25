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
	"gorm.io/gorm"

	"bike-enroll/server/internal/config"
	"bike-enroll/server/internal/model"
	paymentpkg "bike-enroll/server/internal/pkg/payment"
	"bike-enroll/server/internal/repository"
	adminrepo "bike-enroll/server/internal/repository/admin"
	enrollmentrepo "bike-enroll/server/internal/repository/enrollment"
	lockrepo "bike-enroll/server/internal/repository/lock"
	paymentrepo "bike-enroll/server/internal/repository/payment"
	adminsvc "bike-enroll/server/internal/service/admin"
	enrollmentsvc "bike-enroll/server/internal/service/enrollment"
	paymentsvc "bike-enroll/server/internal/service/payment"
)

// TestRegistrationSwitchDoesNotTouchFlows 覆盖报名开关的四条要求：
// 关闭后新锁定被拒；已有 active lock 仍可提交；pending_payment 仍可支付；重新开放后恢复锁定。
func TestRegistrationSwitchDoesNotTouchFlows(t *testing.T) {
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

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	adminService := adminsvc.New(adminrepo.NewGORM(db, rdb), logger)
	enrollService := enrollmentsvc.New(
		lockrepo.NewGORM(db), lockrepo.NewRedisStore(rdb), enrollmentrepo.NewGORM(db),
		15*time.Minute, logger,
	)
	payService := paymentsvc.New(paymentrepo.NewGORM(db), logger)
	ctx := context.Background()
	now := time.Now()

	eventDetail, err := adminService.CreateEvent(ctx, adminrepo.CreateEventParams{
		Title:                "报名开关集成测试",
		RegistrationOpenAt:   now.Add(-24 * time.Hour),
		RegistrationDeadline: now.Add(12 * time.Hour),
		StartTime:            now.Add(24 * time.Hour),
		EndTime:              now.Add(26 * time.Hour),
		Location:             "杭州",
		Description:          "desc", Rules: "", Now: now,
	})
	if err != nil {
		t.Fatalf("创建赛事失败: %v", err)
	}
	eventID := eventDetail.Event.ID
	groupResult, err := adminService.CreateGroup(ctx, adminrepo.CreateGroupParams{
		EventID: eventID, Name: "开关组", Price: 1000, TotalSlots: 5,
		StartTime: now.Add(24 * time.Hour), FormSchema: map[string]any{},
		DisclaimerVersion: "v1", Now: now,
	})
	if err != nil {
		t.Fatalf("创建组别失败: %v", err)
	}
	groupID := groupResult.Group.ID
	redisKey := "group:" + groupID + ":remaining"
	t.Cleanup(func() {
		db.Where("group_id = ?", groupID).Delete(&model.DisclaimerSignature{})
		db.Where("group_id = ?", groupID).Delete(&model.Order{})
		db.Where("group_id = ?", groupID).Delete(&model.Enrollment{})
		db.Where("group_id = ?", groupID).Delete(&model.EnrollmentLock{})
		db.Where("event_id = ?", eventID).Delete(&model.Group{})
		db.Where("id = ?", eventID).Delete(&model.Event{})
		rdb.Del(context.Background(), redisKey)
	})

	// 关闭前先创建一个 active lock，后续验证关闭不影响它提交。
	userA := uuid.NewString()
	lockA, err := enrollService.Lock(ctx, userA, groupID)
	if err != nil {
		t.Fatalf("关闭前锁定失败: %v", err)
	}

	beforeLocks, beforeEnrollments, beforeOrders := counts(t, db, groupID)

	// 1) 关闭报名：幂等，状态 closed，原因落库。
	reason := "临时维护"
	state, err := adminService.CloseEventRegistration(ctx, "admin-switch", eventID, reason)
	if err != nil {
		t.Fatalf("关闭报名失败: %v", err)
	}
	if state.Status != model.RegistrationClosed || !state.Event.RegistrationManuallyClosed {
		t.Fatalf("关闭后状态异常: status=%s manual=%v", state.Status, state.Event.RegistrationManuallyClosed)
	}
	if state.Event.RegistrationClosedReason == nil || *state.Event.RegistrationClosedReason != reason {
		t.Fatalf("关闭原因 = %v，期望 %q", state.Event.RegistrationClosedReason, reason)
	}
	again, err := adminService.CloseEventRegistration(ctx, "admin-switch", eventID, "")
	if err != nil {
		t.Fatalf("重复关闭失败: %v", err)
	}
	if again.Status != model.RegistrationClosed || !again.Event.RegistrationManuallyClosed {
		t.Fatalf("重复关闭状态异常: %+v", again)
	}
	if afterLocks, afterEnrollments, afterOrders := counts(t, db, groupID); afterLocks != beforeLocks ||
		afterEnrollments != beforeEnrollments || afterOrders != beforeOrders {
		t.Fatalf("关闭报名修改了业务数据: locks %d->%d, enrollments %d->%d, orders %d->%d",
			beforeLocks, afterLocks, beforeEnrollments, afterEnrollments, beforeOrders, afterOrders)
	}

	// 2) 关闭后新锁定被拒。
	if _, err := enrollService.Lock(ctx, uuid.NewString(), groupID); !errors.Is(err, enrollmentsvc.ErrEventNotOpen) {
		t.Fatalf("关闭后 Lock err = %v，期望 ErrEventNotOpen", err)
	}

	// 3) 关闭后已有 active lock 的提交同样被开关拦截；lock 本身不被消费。
	submitInput := enrollmentsvc.SubmitInput{
		LockToken:          lockA.Token,
		GroupID:            groupID,
		FormData:           map[string]any{"name": "开关测试"},
		DisclaimerVersion:  "v1",
		DisclaimerSignedAt: now,
		ClientIP:           "127.0.0.1",
		UserAgent:          "registration-switch-test",
	}
	if _, err := enrollService.Submit(ctx, userA, submitInput); !errors.Is(err, enrollmentsvc.ErrEventNotOpen) {
		t.Fatalf("关闭后提交已有 lock err = %v，期望 ErrEventNotOpen", err)
	}
	var lockRow model.EnrollmentLock
	if err := db.Where("token = ?", lockA.Token).First(&lockRow).Error; err != nil {
		t.Fatalf("读取 lock 失败: %v", err)
	}
	if lockRow.Status != "active" {
		t.Fatalf("被拒绝的 submit 不应消费 lock，状态 = %q", lockRow.Status)
	}

	// 4) 重新开放后，同一把 lock 仍可提交（说明关闭/开放未破坏 lock）。
	if _, err := adminService.OpenEventRegistration(ctx, "admin-switch", eventID); err != nil {
		t.Fatalf("重新开放失败: %v", err)
	}
	submit, err := enrollService.Submit(ctx, userA, submitInput)
	if err != nil {
		t.Fatalf("重新开放后提交已有 lock 失败: %v", err)
	}
	if submit.Enrollment.Status != "pending_payment" || submit.Order.Status != "pending_payment" {
		t.Fatalf("提交后状态异常: enrollment=%s order=%s",
			submit.Enrollment.Status, submit.Order.Status)
	}

	// 5) 再次关闭后，pending_payment 订单仍可支付成功。
	if _, err := adminService.CloseEventRegistration(ctx, "admin-switch", eventID, "支付阶段关闭"); err != nil {
		t.Fatalf("支付阶段关闭失败: %v", err)
	}
	if err := payService.HandleNotify(ctx, &paymentpkg.NotifyResult{
		EventType:     "TRANSACTION.SUCCESS",
		OutTradeNo:    submit.Order.ID,
		TransactionID: "wx-switch-test-1",
		TradeState:    "SUCCESS",
		Amount:        paymentpkg.NotifyAmount{Total: submit.Order.Amount, Currency: "CNY"},
	}); err != nil {
		t.Fatalf("关闭期间支付回调失败: %v", err)
	}
	var order model.Order
	if err := db.Where("id = ?", submit.Order.ID).First(&order).Error; err != nil {
		t.Fatalf("读取订单失败: %v", err)
	}
	if order.Status != "paid" {
		t.Fatalf("订单状态 = %q，期望 paid", order.Status)
	}
	var enrollment model.Enrollment
	if err := db.Where("id = ?", submit.Enrollment.ID).First(&enrollment).Error; err != nil {
		t.Fatalf("读取报名失败: %v", err)
	}
	if enrollment.Status != "paid" {
		t.Fatalf("报名状态 = %q，期望 paid", enrollment.Status)
	}

	// 6) 重新开放：幂等，lock 恢复可用。
	openState, err := adminService.OpenEventRegistration(ctx, "admin-switch", eventID)
	if err != nil {
		t.Fatalf("重新开放失败: %v", err)
	}
	if openState.Status != model.RegistrationOpen || openState.Event.RegistrationManuallyClosed {
		t.Fatalf("重新开放后状态异常: %+v", openState)
	}
	if openState.Event.RegistrationClosedReason != nil {
		t.Fatalf("重新开放后应清空关闭原因，实际 = %v", openState.Event.RegistrationClosedReason)
	}
	openAgain, err := adminService.OpenEventRegistration(ctx, "admin-switch", eventID)
	if err != nil {
		t.Fatalf("重复开放失败: %v", err)
	}
	if openAgain.Status != model.RegistrationOpen {
		t.Fatalf("重复开放状态 = %s，期望 open", openAgain.Status)
	}
	if _, err := enrollService.Lock(ctx, uuid.NewString(), groupID); err != nil {
		t.Fatalf("重新开放后 Lock 失败: %v", err)
	}

	// 6) 截止时间已过：重新开放返回 REGISTRATION_DEADLINE_PASSED，且不修改 manual 标记。
	past := now.Add(-time.Hour)
	if err := db.Model(&model.Event{}).Where("id = ?", eventID).
		Update("registration_deadline", past).Error; err != nil {
		t.Fatalf("设置截止时间失败: %v", err)
	}
	if _, err := adminService.CloseEventRegistration(ctx, "admin-switch", eventID, "再次关闭"); err != nil {
		t.Fatalf("再次关闭失败: %v", err)
	}
	if _, err := adminService.OpenEventRegistration(ctx, "admin-switch", eventID); !errors.Is(err, adminrepo.ErrRegistrationDeadlinePassed) {
		t.Fatalf("截止后开放 err = %v，期望 ErrRegistrationDeadlinePassed", err)
	}
	var event model.Event
	if err := db.Where("id = ?", eventID).First(&event).Error; err != nil {
		t.Fatalf("读取赛事失败: %v", err)
	}
	if !event.RegistrationManuallyClosed {
		t.Fatalf("截止后开放失败不应清除手动关闭标记")
	}
}

func counts(t *testing.T, db *gorm.DB, groupID string) (locks, enrollments, orders int64) {
	t.Helper()
	db.Model(&model.EnrollmentLock{}).Where("group_id = ?", groupID).Count(&locks)
	db.Model(&model.Enrollment{}).Where("group_id = ?", groupID).Count(&enrollments)
	db.Model(&model.Order{}).Where("group_id = ?", groupID).Count(&orders)
	return locks, enrollments, orders
}
