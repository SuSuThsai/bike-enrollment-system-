//go:build integration

package event_test

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
	eventrepo "bike-enroll/server/internal/repository/event"
	lockrepo "bike-enroll/server/internal/repository/lock"
	enrollmentsvc "bike-enroll/server/internal/service/enrollment"
	eventsvc "bike-enroll/server/internal/service/event"
)

// noopSlots 仅实现 SlotReader；本用例的赛事没有组别，Detail 不会读取 Redis。
type noopSlots struct{}

func (noopSlots) GetRemaining(context.Context, string) (int, bool, error) {
	return 0, false, nil
}

// TestRegistrationStatusDerivation 覆盖五种报名状态推导场景：
// 手动关闭 / 未开始 / 截止过期 / 赛事取消 / 正常开放。
func TestRegistrationStatusDerivation(t *testing.T) {
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

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := eventsvc.New(eventrepo.NewGORM(db), noopSlots{}, logger)
	ctx := context.Background()
	now := time.Now()

	openAtFuture := now.Add(time.Hour)
	openAtPast := now.Add(-2 * time.Hour)
	deadlinePast := now.Add(-time.Hour)
	deadlineFuture := now.Add(2 * time.Hour)
	reason := "设备检修"

	type fixture struct {
		name  string
		event *model.Event
		want  model.RegistrationStatus
		// public 表示是否应出现在公开详情/列表中（cancelled 对外不可见）。
		public bool
	}
	fixtures := []fixture{
		{
			name: "manual_closed",
			event: &model.Event{
				LifecycleStatus:            model.LifecyclePublished,
				RegistrationManuallyClosed: true,
				RegistrationClosedReason:   &reason,
			},
			want:   model.RegistrationClosed,
			public: true,
		},
		{
			name: "not_started",
			event: &model.Event{
				LifecycleStatus:    model.LifecyclePublished,
				RegistrationOpenAt: &openAtFuture,
			},
			want:   model.RegistrationNotStarted,
			public: true,
		},
		{
			name: "deadline_passed",
			event: &model.Event{
				LifecycleStatus:      model.LifecyclePublished,
				RegistrationOpenAt:   &openAtPast,
				RegistrationDeadline: &deadlinePast,
			},
			want:   model.RegistrationClosed,
			public: true,
		},
		{
			name: "cancelled",
			event: &model.Event{
				LifecycleStatus:      model.LifecycleCancelled,
				RegistrationOpenAt:   &openAtPast,
				RegistrationDeadline: &deadlineFuture,
			},
			want:   model.RegistrationClosed,
			public: false,
		},
		{
			name: "open",
			event: &model.Event{
				LifecycleStatus:      model.LifecyclePublished,
				RegistrationOpenAt:   &openAtPast,
				RegistrationDeadline: &deadlineFuture,
			},
			want:   model.RegistrationOpen,
			public: true,
		},
	}

	ids := make(map[string]string, len(fixtures))
	for _, f := range fixtures {
		event := f.event
		event.ID = uuid.NewString()
		event.Title = "状态推导-" + f.name
		event.CoverURL = ""
		event.StartTime = now.Add(24 * time.Hour)
		event.EndTime = now.Add(26 * time.Hour)
		event.Location = "上海"
		event.Description = "desc"
		event.Rules = ""
		event.CreatedAt = now
		event.UpdatedAt = now
		if err := db.Create(event).Error; err != nil {
			t.Fatalf("创建赛事 %s 失败: %v", f.name, err)
		}
		eventID := event.ID
		ids[f.name] = eventID
		t.Cleanup(func() {
			db.Where("event_id = ?", eventID).Delete(&model.Group{})
			db.Where("id = ?", eventID).Delete(&model.Event{})
		})

		if got := model.ResolveRegistrationStatus(event, now); got != f.want {
			t.Errorf("%s: ResolveRegistrationStatus = %q，期望 %q", f.name, got, f.want)
		}

		detail, err := service.Detail(ctx, eventID)
		if !f.public {
			if !errors.Is(err, eventsvc.ErrEventNotFound) {
				t.Errorf("%s: 公开详情 err = %v，期望 ErrEventNotFound", f.name, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: 公开详情失败: %v", f.name, err)
		}
		if detail.Event.RegistrationStatus != f.want {
			t.Errorf("%s: registration_status = %q，期望 %q", f.name, detail.Event.RegistrationStatus, f.want)
		}
		if f.name == "manual_closed" {
			if detail.Event.Event.RegistrationClosedReason == nil ||
				*detail.Event.Event.RegistrationClosedReason != reason {
				t.Errorf("%s: registration_closed_reason = %v，期望 %q",
					f.name, detail.Event.Event.RegistrationClosedReason, reason)
			}
		}
	}

	// 列表筛选必须与推导规则一致。
	for _, f := range fixtures {
		if !f.public {
			continue
		}
		list, err := service.List(ctx, f.want.String(), 1, 100)
		if err != nil {
			t.Fatalf("List(%s) 失败: %v", f.want, err)
		}
		found := false
		for _, view := range list.Events {
			if view.Event.ID == ids[f.name] {
				found = true
				if view.RegistrationStatus != f.want {
					t.Errorf("List(%s) 中 %s 的 registration_status = %q", f.want, f.name, view.RegistrationStatus)
				}
			}
		}
		if !found {
			t.Errorf("List(%s) 未包含 %s", f.want, f.name)
		}
	}

	// 锁名额校验：非 open 状态必须返回 EVENT_NOT_OPEN。
	lockService := enrollmentsvc.New(lockrepo.NewGORM(db), nil, nil, time.Minute, logger)
	for _, name := range []string{"manual_closed", "cancelled", "not_started", "deadline_passed"} {
		group := &model.Group{
			ID: uuid.NewString(), EventID: ids[name], Name: "锁校验组",
			Price: 1, TotalSlots: 1, StartTime: now, Status: "open",
			FormSchema: map[string]any{}, DisclaimerVersion: "v1",
			CreatedAt: now, UpdatedAt: now,
		}
		if err := db.Create(group).Error; err != nil {
			t.Fatalf("%s: 创建组别失败: %v", name, err)
		}
		if _, err := lockService.Lock(ctx, uuid.NewString(), group.ID); !errors.Is(err, enrollmentsvc.ErrEventNotOpen) {
			t.Errorf("%s: Lock err = %v，期望 ErrEventNotOpen", name, err)
		}
	}
}
