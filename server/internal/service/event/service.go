// Package event 实现赛事列表与详情业务逻辑。
package event

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"bike-enroll/server/internal/model"
	eventrepo "bike-enroll/server/internal/repository/event"
	"bike-enroll/server/internal/requestid"
)

// ErrEventNotFound 表示赛事不存在或不可公开访问。
var ErrEventNotFound = errors.New("event not found")

// ErrRemainingUnavailable 表示 Redis 中缺少组别剩余名额，或值非法。
//
// 剩余名额只认 Redis；不得回退到 MySQL total_slots。
var ErrRemainingUnavailable = errors.New("group remaining unavailable")

// ErrInvalidArgument 表示分页或筛选参数非法。
var ErrInvalidArgument = errors.New("invalid argument")

// Service 聚合赛事查询依赖。
type Service struct {
	repo   eventrepo.Repository
	slots  eventrepo.SlotReader
	logger *slog.Logger
}

// New 创建赛事服务。
func New(repo eventrepo.Repository, slots eventrepo.SlotReader, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: repo, slots: slots, logger: logger}
}

// EventView 是赛事与其报名状态的组合视图。
type EventView struct {
	Event              model.Event
	RegistrationStatus model.RegistrationStatus
}

// ListResult 是赛事列表结果。
type ListResult struct {
	Events    []EventView
	MinPrices map[string]int
	Total     int64
}

// GroupView 是带实时剩余名额的组别视图。
type GroupView struct {
	Group          model.Group
	RemainingSlots int
	Status         string
}

// DetailResult 是赛事详情结果。
type DetailResult struct {
	Event  EventView
	Groups []GroupView
}

// List 返回分页公开赛事。
//
// registrationStatus 为空表示不筛选；非空时只接受 not_started / open / closed。
func (s *Service) List(ctx context.Context, registrationStatus string, page, pageSize int) (*ListResult, error) {
	if page < 1 || pageSize < 1 || pageSize > 100 {
		return nil, ErrInvalidArgument
	}
	if registrationStatus != "" && !model.RegistrationStatus(registrationStatus).Valid() {
		return nil, ErrInvalidArgument
	}

	offset := (page - 1) * pageSize
	events, total, err := s.repo.ListPublic(ctx, registrationStatus, offset, pageSize)
	if err != nil {
		return nil, fmt.Errorf("list public events: %w", err)
	}

	ids := make([]string, 0, len(events))
	views := make([]EventView, 0, len(events))
	now := time.Now()
	for _, event := range events {
		ids = append(ids, event.ID)
		views = append(views, EventView{
			Event:              event,
			RegistrationStatus: model.ResolveRegistrationStatus(&event, now),
		})
	}
	prices, err := s.repo.MinPrices(ctx, ids)
	if err != nil {
		return nil, err
	}

	return &ListResult{Events: views, MinPrices: prices, Total: total}, nil
}

// Detail 返回赛事详情，组别 remaining_slots 只从 Redis 读取。
//
// Redis key 缺失或值为负时返回 ErrRemainingUnavailable；不使用 MySQL total_slots 回退。
func (s *Service) Detail(ctx context.Context, eventID string) (*DetailResult, error) {
	event, groups, err := s.repo.GetDetail(ctx, eventID)
	if err != nil {
		if errors.Is(err, eventrepo.ErrEventNotFound) {
			return nil, ErrEventNotFound
		}
		return nil, fmt.Errorf("get event detail: %w", err)
	}

	views := make([]GroupView, 0, len(groups))
	for _, group := range groups {
		remaining, exists, err := s.slots.GetRemaining(ctx, group.ID)
		if err != nil {
			s.logger.ErrorContext(ctx, "read group remaining from Redis failed",
				"request_id", requestid.FromContext(ctx),
				"group_id", group.ID,
				"error", err,
			)
			return nil, fmt.Errorf("read group remaining: %w", err)
		}

		if !exists {
			s.logger.ErrorContext(ctx, "group remaining missing in Redis",
				"request_id", requestid.FromContext(ctx),
				"group_id", group.ID,
			)
			return nil, fmt.Errorf("%w: group %s", ErrRemainingUnavailable, group.ID)
		}
		if remaining < 0 {
			s.logger.ErrorContext(ctx, "group remaining is negative",
				"request_id", requestid.FromContext(ctx),
				"group_id", group.ID,
				"remaining_slots", remaining,
			)
			return nil, fmt.Errorf("%w: group %s has negative remaining", ErrRemainingUnavailable, group.ID)
		}

		views = append(views, GroupView{
			Group:          group,
			RemainingSlots: remaining,
			Status:         deriveGroupStatus(group.Status, remaining),
		})
	}

	return &DetailResult{
		Event: EventView{
			Event:              *event,
			RegistrationStatus: model.ResolveRegistrationStatus(event, time.Now()),
		},
		Groups: views,
	}, nil
}

func deriveGroupStatus(stored string, remaining int) string {
	if stored == "closed" {
		return "closed"
	}
	if remaining <= 0 {
		return "sold_out"
	}
	return "open"
}
