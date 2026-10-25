// Package enrollment 实现报名相关业务逻辑。
package enrollment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"bike-enroll/server/internal/model"
	enrollmentrepo "bike-enroll/server/internal/repository/enrollment"
	lockrepo "bike-enroll/server/internal/repository/lock"
	"bike-enroll/server/internal/requestid"
)

// 锁定/提交相关错误，供 handler 映射为 openapi.yaml 错误码。
var (
	ErrInvalidArgument           = errors.New("invalid argument")
	ErrGroupNotFound             = errors.New("group not found")
	ErrEventNotOpen              = errors.New("event not open")
	ErrGroupClosed               = errors.New("group closed")
	ErrGroupSoldOut              = errors.New("group sold out")
	ErrRateLimited               = errors.New("rate limited")
	ErrLockInvalid               = errors.New("lock invalid")
	ErrLockExpired               = errors.New("lock expired")
	ErrLockAlreadyConsumed       = errors.New("lock already consumed")
	ErrEnrollmentDuplicate       = errors.New("enrollment duplicate")
	ErrDisclaimerVersionMismatch = errors.New("disclaimer version mismatch")
	ErrEnrollmentNotFound        = errors.New("enrollment not found")
)

const defaultLockTTL = 15 * time.Minute

// Service 聚合锁名额与提交报名所需依赖。
type Service struct {
	repo       lockrepo.Repository
	submitRepo enrollmentrepo.Repository
	slots      lockrepo.SlotStore
	lockTTL    time.Duration
	logger     *slog.Logger
}

// New 创建报名服务。
func New(
	repo lockrepo.Repository,
	slots lockrepo.SlotStore,
	submitRepo enrollmentrepo.Repository,
	lockTTL time.Duration,
	logger *slog.Logger,
) *Service {
	if lockTTL <= 0 {
		lockTTL = defaultLockTTL
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: repo, slots: slots, submitRepo: submitRepo, lockTTL: lockTTL, logger: logger}
}

// LockResult 是锁定成功结果。
type LockResult struct {
	Token          string
	ExpiresAt      time.Time
	RemainingSlots int
}

// Lock 执行 Lock 状态机的“无 → active”迁移。
func (s *Service) Lock(ctx context.Context, userID, groupID string) (*LockResult, error) {
	if userID == "" || groupID == "" {
		return nil, ErrInvalidArgument
	}

	group, event, err := s.repo.GetGroupForLock(ctx, groupID)
	if err != nil {
		if errors.Is(err, lockrepo.ErrGroupNotFound) {
			return nil, ErrGroupNotFound
		}
		return nil, fmt.Errorf("get group for lock: %w", err)
	}

	if err := s.ensureRegistrationOpen(event, time.Now()); err != nil {
		return nil, err
	}
	switch group.Status {
	case "open":
	case "closed":
		return nil, ErrGroupClosed
	case "sold_out":
		return nil, ErrGroupSoldOut
	default:
		return nil, ErrGroupClosed
	}

	acquired, err := s.slots.AcquireUserLock(ctx, userID, groupID)
	if err != nil {
		return nil, fmt.Errorf("acquire user lock: %w", err)
	}
	if !acquired {
		return nil, ErrRateLimited
	}

	remaining, err := s.slots.Reserve(ctx, groupID)
	if err != nil {
		if errors.Is(err, lockrepo.ErrSoldOut) {
			return nil, ErrGroupSoldOut
		}
		return nil, fmt.Errorf("reserve group slot: %w", err)
	}

	now := time.Now()
	expiresAt := now.Add(s.lockTTL)
	lock := &model.EnrollmentLock{
		Token:     uuid.NewString(),
		UserID:    userID,
		GroupID:   groupID,
		Status:    "active",
		ExpiresAt: expiresAt,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.repo.Create(ctx, lock); err != nil {
		if rollbackErr := s.slots.Rollback(ctx, groupID, lock.Token); rollbackErr != nil {
			s.logger.ErrorContext(ctx, "rollback group slot failed",
				"request_id", requestid.FromContext(ctx),
				"group_id", groupID,
				"error", rollbackErr,
			)
		}
		return nil, fmt.Errorf("create enrollment lock: %w", err)
	}

	// 辅助 key，仅用于过期判断；失败不影响已落库的 active 锁。
	if err := s.slots.SetLockTokenTTL(ctx, lock.Token, s.lockTTL); err != nil {
		s.logger.WarnContext(ctx, "set lock token ttl failed",
			"request_id", requestid.FromContext(ctx),
			"group_id", groupID,
			"error", err,
		)
	}

	return &LockResult{
		Token:          lock.Token,
		ExpiresAt:      expiresAt,
		RemainingSlots: remaining,
	}, nil
}

// SubmitInput 是提交报名输入。
type SubmitInput struct {
	LockToken          string
	GroupID            string
	FormData           map[string]any
	DisclaimerVersion  string
	DisclaimerSignedAt time.Time
	ClientIP           string
	UserAgent          string
}

// SubmitResult 是提交报名成功结果。
type SubmitResult struct {
	Enrollment *model.Enrollment
	Order      *model.Order
}

// Submit 执行 Lock 的 active → consumed 迁移，并创建 Enrollment + Order。
//
// 本方法不操作 Redis：名额已在 Lock 阶段占用。
func (s *Service) Submit(ctx context.Context, userID string, input SubmitInput) (*SubmitResult, error) {
	if userID == "" || input.LockToken == "" || input.GroupID == "" || input.DisclaimerSignedAt.IsZero() {
		return nil, ErrInvalidArgument
	}
	if input.FormData == nil {
		input.FormData = map[string]any{}
	}

	group, err := s.submitRepo.GetGroup(ctx, input.GroupID)
	if err != nil {
		if errors.Is(err, enrollmentrepo.ErrGroupNotFound) {
			return nil, ErrGroupNotFound
		}
		return nil, fmt.Errorf("get group for submit: %w", err)
	}

	// 报名开关必须在消费 lock / 创建订单之前判断：
	// 防止“deadline 前锁定、deadline 后提交”的窗口，也覆盖手动关闭。
	_, event, err := s.repo.GetGroupForLock(ctx, input.GroupID)
	if err != nil {
		if errors.Is(err, lockrepo.ErrGroupNotFound) {
			return nil, ErrGroupNotFound
		}
		return nil, fmt.Errorf("get event for submit: %w", err)
	}
	if err := s.ensureRegistrationOpen(event, time.Now()); err != nil {
		return nil, err
	}

	if input.DisclaimerVersion != group.DisclaimerVersion {
		return nil, ErrDisclaimerVersionMismatch
	}

	now := time.Now()
	result, err := s.submitRepo.Create(ctx, enrollmentrepo.CreateParams{
		LockToken:    input.LockToken,
		UserID:       userID,
		GroupID:      input.GroupID,
		EventID:      group.EventID,
		EnrollmentID: uuid.NewString(),
		// 微信支付 out_trade_no 上限 32 字符，订单号使用无连字符 UUID（32 hex）。
		OrderID:            strings.ReplaceAll(uuid.NewString(), "-", ""),
		Amount:             group.Price,
		FormData:           input.FormData,
		DisclaimerVersion:  input.DisclaimerVersion,
		DisclaimerSignedAt: input.DisclaimerSignedAt,
		ClientIP:           input.ClientIP,
		UserAgent:          input.UserAgent,
		Now:                now,
	})
	if err != nil {
		switch {
		case errors.Is(err, enrollmentrepo.ErrLockInvalid):
			return nil, ErrLockInvalid
		case errors.Is(err, enrollmentrepo.ErrLockExpired):
			return nil, ErrLockExpired
		case errors.Is(err, enrollmentrepo.ErrLockAlreadyConsumed):
			return nil, ErrLockAlreadyConsumed
		case errors.Is(err, enrollmentrepo.ErrDuplicate):
			return nil, ErrEnrollmentDuplicate
		default:
			return nil, fmt.Errorf("create enrollment: %w", err)
		}
	}

	return &SubmitResult{Enrollment: result.Enrollment, Order: result.Order}, nil
}

// ListMine 返回当前用户的全部报名，按创建时间倒序。
func (s *Service) ListMine(ctx context.Context, userID string) ([]model.Enrollment, error) {
	if userID == "" {
		return nil, ErrInvalidArgument
	}
	items, err := s.submitRepo.ListByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list my enrollments: %w", err)
	}
	return items, nil
}

// Detail 返回当前用户的一条报名；不存在或非本人统一返回 ErrEnrollmentNotFound。
func (s *Service) Detail(ctx context.Context, userID, enrollmentID string) (*model.Enrollment, error) {
	if userID == "" || enrollmentID == "" {
		return nil, ErrInvalidArgument
	}
	item, err := s.submitRepo.GetByIDForUser(ctx, enrollmentID, userID)
	if err != nil {
		if errors.Is(err, enrollmentrepo.ErrEnrollmentNotFound) {
			return nil, ErrEnrollmentNotFound
		}
		return nil, fmt.Errorf("get my enrollment: %w", err)
	}
	return item, nil
}
