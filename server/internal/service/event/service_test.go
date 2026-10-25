package event_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"bike-enroll/server/internal/model"
	eventsvc "bike-enroll/server/internal/service/event"
)

type fakeRepository struct {
	event  *model.Event
	groups []model.Group
	err    error
}

func (f *fakeRepository) ListPublic(context.Context, string, int, int) ([]model.Event, int64, error) {
	return nil, 0, f.err
}

func (f *fakeRepository) MinPrices(context.Context, []string) (map[string]int, error) {
	return map[string]int{}, f.err
}

func (f *fakeRepository) GetDetail(context.Context, string) (*model.Event, []model.Group, error) {
	if f.err != nil {
		return nil, nil, f.err
	}
	return f.event, f.groups, nil
}

type fakeSlots struct {
	remaining map[string]int
	present   map[string]bool
	err       error
}

func (f *fakeSlots) GetRemaining(_ context.Context, groupID string) (int, bool, error) {
	if f.err != nil {
		return 0, false, f.err
	}
	return f.remaining[groupID], f.present[groupID], nil
}

func testEvent() *model.Event {
	return &model.Event{
		ID:              "event-1",
		Title:           "测试赛事",
		CoverURL:        "https://example.com/cover.png",
		StartTime:       time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC),
		Location:        "测试地点",
		LifecycleStatus: model.LifecyclePublished,
	}
}

func testGroup() model.Group {
	return model.Group{
		ID:                "group-1",
		EventID:           "event-1",
		Name:              "大众组",
		Price:             10000,
		TotalSlots:        100,
		StartTime:         time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC),
		Status:            "open",
		FormSchema:        map[string]any{"fields": []any{}},
		DisclaimerVersion: "v1",
	}
}

func TestDetailUsesRedisRemaining(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	repo := &fakeRepository{event: testEvent(), groups: []model.Group{testGroup()}}
	slots := &fakeSlots{
		remaining: map[string]int{"group-1": 42},
		present:   map[string]bool{"group-1": true},
	}
	service := eventsvc.New(repo, slots, logger)

	result, err := service.Detail(context.Background(), "event-1")
	if err != nil {
		t.Fatalf("Detail 失败: %v", err)
	}
	if got := result.Groups[0].RemainingSlots; got != 42 {
		t.Errorf("remaining_slots = %d，期望 Redis 值 42", got)
	}
	if got := result.Groups[0].Status; got != "open" {
		t.Errorf("status = %q，期望 open", got)
	}
}

func TestDetailMissingRedisReturnsError(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	repo := &fakeRepository{event: testEvent(), groups: []model.Group{testGroup()}}
	slots := &fakeSlots{
		remaining: map[string]int{},
		present:   map[string]bool{},
	}
	service := eventsvc.New(repo, slots, logger)

	result, err := service.Detail(context.Background(), "event-1")
	if !errors.Is(err, eventsvc.ErrRemainingUnavailable) {
		t.Fatalf("err = %v，期望 ErrRemainingUnavailable", err)
	}
	if result != nil {
		t.Errorf("result = %+v，期望 nil", result)
	}
	logOutput := logs.String()
	for _, expected := range []string{"level=ERROR", "group_id=group-1", "missing in Redis"} {
		if !strings.Contains(logOutput, expected) {
			t.Errorf("日志缺少 %q，实际: %s", expected, logOutput)
		}
	}
}

func TestDetailNegativeRemainingReturnsError(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	repo := &fakeRepository{event: testEvent(), groups: []model.Group{testGroup()}}
	slots := &fakeSlots{
		remaining: map[string]int{"group-1": -1},
		present:   map[string]bool{"group-1": true},
	}
	service := eventsvc.New(repo, slots, logger)

	result, err := service.Detail(context.Background(), "event-1")
	if !errors.Is(err, eventsvc.ErrRemainingUnavailable) {
		t.Fatalf("err = %v，期望 ErrRemainingUnavailable", err)
	}
	if result != nil {
		t.Errorf("result = %+v，期望 nil", result)
	}
	if !strings.Contains(logs.String(), "negative") {
		t.Errorf("日志缺少 negative，实际: %s", logs.String())
	}
}

func TestDetailDerivesSoldOut(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	repo := &fakeRepository{event: testEvent(), groups: []model.Group{testGroup()}}
	slots := &fakeSlots{
		remaining: map[string]int{"group-1": 0},
		present:   map[string]bool{"group-1": true},
	}
	service := eventsvc.New(repo, slots, logger)

	result, err := service.Detail(context.Background(), "event-1")
	if err != nil {
		t.Fatalf("Detail 失败: %v", err)
	}
	if got := result.Groups[0].Status; got != "sold_out" {
		t.Errorf("status = %q，期望 sold_out", got)
	}
}

func TestListValidatesPagination(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	service := eventsvc.New(&fakeRepository{}, &fakeSlots{}, logger)

	if _, err := service.List(context.Background(), "", 1, 101); !errors.Is(err, eventsvc.ErrInvalidArgument) {
		t.Errorf("page_size=101 应返回 ErrInvalidArgument，实际 %v", err)
	}
	if _, err := service.List(context.Background(), "", 0, 20); !errors.Is(err, eventsvc.ErrInvalidArgument) {
		t.Errorf("page=0 应返回 ErrInvalidArgument，实际 %v", err)
	}
	if _, err := service.List(context.Background(), "draft", 1, 20); !errors.Is(err, eventsvc.ErrInvalidArgument) {
		t.Errorf("status=draft 应返回 ErrInvalidArgument，实际 %v", err)
	}
	if _, err := service.List(context.Background(), "", 1, 100); err != nil {
		t.Errorf("page_size=100 应合法，实际 %v", err)
	}
}
