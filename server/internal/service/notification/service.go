// Package notification 实现订阅消息授权与异步通知发送。
package notification

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	notificationrepo "bike-enroll/server/internal/repository/notification"
)

// ErrInvalidArgument 表示订阅模板参数非法。
var ErrInvalidArgument = errors.New("invalid argument")

// Sender 是订阅消息发送抽象，便于单元测试 mock。
type Sender interface {
	SendSubscribeMessage(ctx context.Context, openID, templateID string, data map[string]any) error
}

type notificationTask struct {
	OrderID   string
	RequestID string
}

// Service 负责授权记录和异步发送“报名成功”通知。
type Service struct {
	repo       notificationrepo.Repository
	sender     Sender
	templateID string
	logger     *slog.Logger

	queue chan notificationTask

	mu     sync.RWMutex
	closed bool
	wg     sync.WaitGroup
}

// New 创建通知服务并启动异步 worker。
func New(repo notificationrepo.Repository, sender Sender, templateID string, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	service := &Service{
		repo:       repo,
		sender:     sender,
		templateID: templateID,
		logger:     logger,
		queue:      make(chan notificationTask, 256),
	}
	service.wg.Add(1)
	go service.worker()
	return service
}

// Subscribe 记录用户上报的模板授权；空/重复模板会被忽略。
func (s *Service) Subscribe(ctx context.Context, userID string, templateIDs []string) error {
	templates := make([]string, 0, len(templateIDs))
	seen := make(map[string]struct{}, len(templateIDs))
	for _, raw := range templateIDs {
		templateID := strings.TrimSpace(raw)
		if templateID == "" {
			continue
		}
		if _, ok := seen[templateID]; ok {
			continue
		}
		seen[templateID] = struct{}{}
		templates = append(templates, templateID)
	}
	if len(templates) == 0 {
		return ErrInvalidArgument
	}
	return s.repo.Subscribe(ctx, userID, templates)
}

// EnqueueEnrollmentSuccess 非阻塞投递报名成功通知。
func (s *Service) EnqueueEnrollmentSuccess(orderID, requestID string) {
	if orderID == "" {
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		s.logger.Warn("notification enqueue after close", "request_id", requestID, "order_id", orderID)
		return
	}
	select {
	case s.queue <- notificationTask{OrderID: orderID, RequestID: requestID}:
	default:
		s.logger.Warn("notification queue full; drop notification", "request_id", requestID, "order_id", orderID)
	}
}

// Close 停止接收新任务并等待队列中的通知处理完成。
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.queue)
	}
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) worker() {
	defer s.wg.Done()
	for task := range s.queue {
		s.sendEnrollmentSuccess(task)
	}
}

func (s *Service) sendEnrollmentSuccess(task notificationTask) {
	if s.sender == nil || s.templateID == "" {
		s.logger.Info("enrollment notification skipped: sender/template not configured",
			"request_id", task.RequestID,
			"order_id", task.OrderID,
		)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	data, err := s.repo.GetEnrollmentNotification(ctx, task.OrderID)
	if err != nil {
		s.logger.Error("load enrollment notification failed",
			"request_id", task.RequestID,
			"order_id", task.OrderID,
			"error", err,
		)
		return
	}
	if data.OpenID == "" {
		s.logger.Warn("enrollment notification skipped: missing openid",
			"request_id", task.RequestID,
			"order_id", task.OrderID,
		)
		return
	}

	authorized, err := s.repo.ConsumeAuthorization(ctx, data.UserID, s.templateID)
	if err != nil {
		s.logger.Error("consume notification authorization failed",
			"request_id", task.RequestID,
			"order_id", task.OrderID,
			"error", err,
		)
		return
	}
	if !authorized {
		s.logger.Info("enrollment notification skipped: no unconsumed authorization",
			"request_id", task.RequestID,
			"order_id", task.OrderID,
		)
		return
	}

	if err := s.sender.SendSubscribeMessage(ctx, data.OpenID, s.templateID, templateData(data)); err != nil {
		s.logger.Error("send enrollment notification failed",
			"request_id", task.RequestID,
			"order_id", task.OrderID,
			"error", err,
		)
		return
	}
	s.logger.Info("enrollment notification sent", "request_id", task.RequestID, "order_id", task.OrderID)
}

func templateData(data *notificationrepo.EnrollmentNotification) map[string]any {
	paidAt := ""
	if data.PaidAt != nil {
		paidAt = data.PaidAt.Format("2006-01-02 15:04")
	}
	return map[string]any{
		"thing1":            map[string]string{"value": truncateRunes(data.EventTitle, 20)},
		"time2":             map[string]string{"value": paidAt},
		"thing3":            map[string]string{"value": truncateRunes(data.GroupName, 20)},
		"character_string4": map[string]string{"value": data.OrderID},
	}
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
