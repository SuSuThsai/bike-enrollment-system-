package notification_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	notificationrepo "bike-enroll/server/internal/repository/notification"
	notificationsvc "bike-enroll/server/internal/service/notification"
)

type fakeRepository struct {
	subscribeUser string
	templates     []string
	subscribeErr  error

	notification    *notificationrepo.EnrollmentNotification
	notificationErr error

	authorized      bool
	consumeUser     string
	consumeTemplate string
	consumeErr      error
}

func (f *fakeRepository) Subscribe(_ context.Context, userID string, templateIDs []string) error {
	f.subscribeUser = userID
	f.templates = append([]string(nil), templateIDs...)
	return f.subscribeErr
}

func (f *fakeRepository) ConsumeAuthorization(_ context.Context, userID, templateID string) (bool, error) {
	f.consumeUser = userID
	f.consumeTemplate = templateID
	return f.authorized, f.consumeErr
}

func (f *fakeRepository) GetEnrollmentNotification(context.Context, string) (*notificationrepo.EnrollmentNotification, error) {
	return f.notification, f.notificationErr
}

type fakeSender struct {
	called     bool
	openID     string
	templateID string
	data       map[string]any
	err        error
}

func (f *fakeSender) SendSubscribeMessage(_ context.Context, openID, templateID string, data map[string]any) error {
	f.called = true
	f.openID = openID
	f.templateID = templateID
	f.data = data
	return f.err
}

func TestSubscribeNormalizesTemplates(t *testing.T) {
	repo := &fakeRepository{}
	service := notificationsvc.New(repo, nil, "tpl", nil)
	defer service.Close(context.Background())

	if err := service.Subscribe(context.Background(), "user-1", []string{" tpl-1 ", "tpl-1", "", "tpl-2"}); err != nil {
		t.Fatalf("Subscribe 失败: %v", err)
	}
	if repo.subscribeUser != "user-1" {
		t.Errorf("userID = %q", repo.subscribeUser)
	}
	if len(repo.templates) != 2 || repo.templates[0] != "tpl-1" || repo.templates[1] != "tpl-2" {
		t.Errorf("模板去重/去空格错误: %+v", repo.templates)
	}
}

func TestSubscribeRejectsEmptyTemplates(t *testing.T) {
	service := notificationsvc.New(&fakeRepository{}, nil, "tpl", nil)
	defer service.Close(context.Background())

	if err := service.Subscribe(context.Background(), "user-1", []string{"", "  "}); !errors.Is(err, notificationsvc.ErrInvalidArgument) {
		t.Fatalf("err = %v，期望 ErrInvalidArgument", err)
	}
}

func TestEnqueueSendsWhenAuthorized(t *testing.T) {
	paidAt := time.Now()
	repo := &fakeRepository{
		authorized: true,
		notification: &notificationrepo.EnrollmentNotification{
			UserID:     "user-1",
			OpenID:     "openid-1",
			EventTitle: "测试赛事",
			GroupName:  "大众组",
			OrderID:    "order-1",
			Amount:     10000,
			PaidAt:     &paidAt,
		},
	}
	sender := &fakeSender{}
	service := notificationsvc.New(repo, sender, "tpl-enroll", nil)

	service.EnqueueEnrollmentSuccess("order-1", "req-1")
	if err := service.Close(context.Background()); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}

	if !sender.called || sender.openID != "openid-1" || sender.templateID != "tpl-enroll" {
		t.Errorf("发送参数错误: %+v", sender)
	}
	if repo.consumeUser != "user-1" || repo.consumeTemplate != "tpl-enroll" {
		t.Errorf("授权消费参数错误: user=%q template=%q", repo.consumeUser, repo.consumeTemplate)
	}
	if sender.data["thing1"] == nil || sender.data["time2"] == nil || sender.data["thing3"] == nil {
		t.Errorf("模板数据不完整: %+v", sender.data)
	}
}

func TestEnqueueSkipsWithoutAuthorization(t *testing.T) {
	repo := &fakeRepository{
		authorized: false,
		notification: &notificationrepo.EnrollmentNotification{
			UserID:  "user-1",
			OpenID:  "openid-1",
			OrderID: "order-1",
		},
	}
	sender := &fakeSender{}
	service := notificationsvc.New(repo, sender, "tpl-enroll", nil)

	service.EnqueueEnrollmentSuccess("order-1", "req-1")
	if err := service.Close(context.Background()); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}
	if sender.called {
		t.Error("没有未消费授权时不应发送")
	}
}

func TestEnqueueSenderFailureOnlyLogs(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	repo := &fakeRepository{
		authorized: true,
		notification: &notificationrepo.EnrollmentNotification{
			UserID:  "user-1",
			OpenID:  "openid-1",
			OrderID: "order-1",
		},
	}
	sender := &fakeSender{err: errors.New("wechat subscribe failed")}
	service := notificationsvc.New(repo, sender, "tpl-enroll", logger)

	service.EnqueueEnrollmentSuccess("order-1", "req-1")
	if err := service.Close(context.Background()); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}
	if !strings.Contains(logs.String(), "send enrollment notification failed") {
		t.Errorf("发送失败应记录日志: %s", logs.String())
	}
}
