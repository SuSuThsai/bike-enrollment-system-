// Package enrollment 提供报名提交所需的数据访问。
package enrollment

import (
	"context"
	"errors"
	"fmt"
	"time"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"bike-enroll/server/internal/model"
)

// 提交报名相关错误，供 service 映射。
var (
	ErrGroupNotFound       = errors.New("group not found")
	ErrLockInvalid         = errors.New("lock invalid")
	ErrLockExpired         = errors.New("lock expired")
	ErrLockAlreadyConsumed = errors.New("lock already consumed")
	ErrDuplicate           = errors.New("enrollment duplicate")
	ErrEnrollmentNotFound  = errors.New("enrollment not found")
)

// CreateParams 是提交报名的持久化参数。
type CreateParams struct {
	LockToken          string
	UserID             string
	GroupID            string
	EventID            string
	EnrollmentID       string
	OrderID            string
	Amount             int
	FormData           map[string]any
	DisclaimerVersion  string
	DisclaimerSignedAt time.Time
	ClientIP           string
	UserAgent          string
	Now                time.Time
}

// CreateResult 是提交报名成功后的记录。
type CreateResult struct {
	Enrollment *model.Enrollment
	Order      *model.Order
}

// Repository 是报名提交与查询持久化接口。
type Repository interface {
	GetGroup(ctx context.Context, groupID string) (*model.Group, error)
	Create(ctx context.Context, params CreateParams) (*CreateResult, error)
	ListByUser(ctx context.Context, userID string) ([]model.Enrollment, error)
	GetByIDForUser(ctx context.Context, enrollmentID, userID string) (*model.Enrollment, error)
}

// GORMRepository 是 Repository 的 MySQL/GORM 实现。
type GORMRepository struct {
	db *gorm.DB
}

// NewGORM 创建报名仓储。
func NewGORM(db *gorm.DB) *GORMRepository {
	return &GORMRepository{db: db}
}

// GetGroup 返回组别，用于价格与免责协议版本校验。
func (r *GORMRepository) GetGroup(ctx context.Context, groupID string) (*model.Group, error) {
	var group model.Group
	if err := r.db.WithContext(ctx).Where("id = ?", groupID).First(&group).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrGroupNotFound
		}
		return nil, fmt.Errorf("find group: %w", err)
	}
	return &group, nil
}

// ListByUser 返回用户全部报名，按创建时间倒序。
func (r *GORMRepository) ListByUser(ctx context.Context, userID string) ([]model.Enrollment, error) {
	items := make([]model.Enrollment, 0)
	if err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("created_at DESC, id DESC").
		Find(&items).Error; err != nil {
		return nil, fmt.Errorf("list enrollments by user: %w", err)
	}
	return items, nil
}

// GetByIDForUser 返回属于指定用户的报名；不存在或不属于该用户时统一返回 ErrEnrollmentNotFound。
func (r *GORMRepository) GetByIDForUser(ctx context.Context, enrollmentID, userID string) (*model.Enrollment, error) {
	var item model.Enrollment
	if err := r.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", enrollmentID, userID).
		First(&item).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrEnrollmentNotFound
		}
		return nil, fmt.Errorf("get enrollment by id for user: %w", err)
	}
	return &item, nil
}

// Create 在单个事务内消费 lock、创建 Enrollment/Order/免责签署记录。
func (r *GORMRepository) Create(ctx context.Context, params CreateParams) (*CreateResult, error) {
	if params.FormData == nil {
		params.FormData = map[string]any{}
	}

	var result *CreateResult
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		update := tx.Model(&model.EnrollmentLock{}).
			Where(
				"token = ? AND user_id = ? AND group_id = ? AND status = ? AND expires_at > NOW(3)",
				params.LockToken,
				params.UserID,
				params.GroupID,
				"active",
			).
			Updates(map[string]any{
				"status":      "consumed",
				"consumed_at": params.Now,
				"updated_at":  params.Now,
			})
		if update.Error != nil {
			return fmt.Errorf("consume lock: %w", update.Error)
		}
		if update.RowsAffected != 1 {
			return classifyLockFailure(tx, params)
		}

		enrollment := &model.Enrollment{
			ID:        params.EnrollmentID,
			UserID:    params.UserID,
			EventID:   params.EventID,
			GroupID:   params.GroupID,
			OrderID:   params.OrderID,
			Status:    "pending_payment",
			FormData:  params.FormData,
			CreatedAt: params.Now,
			UpdatedAt: params.Now,
		}
		if err := tx.Create(enrollment).Error; err != nil {
			if isDuplicateKey(err) {
				return ErrDuplicate
			}
			return fmt.Errorf("create enrollment: %w", err)
		}

		order := &model.Order{
			ID:           params.OrderID,
			EnrollmentID: params.EnrollmentID,
			UserID:       params.UserID,
			GroupID:      params.GroupID,
			Amount:       params.Amount,
			Status:       "pending_payment",
			CreatedAt:    params.Now,
			UpdatedAt:    params.Now,
		}
		if err := tx.Create(order).Error; err != nil {
			if isDuplicateKey(err) {
				return ErrDuplicate
			}
			return fmt.Errorf("create order: %w", err)
		}

		if err := createDisclaimerSignature(tx, params); err != nil {
			return err
		}

		result = &CreateResult{Enrollment: enrollment, Order: order}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func createDisclaimerSignature(tx *gorm.DB, params CreateParams) error {
	signature := &model.DisclaimerSignature{
		ID:                uuid.NewString(),
		UserID:            params.UserID,
		EnrollmentID:      params.EnrollmentID,
		GroupID:           params.GroupID,
		DisclaimerVersion: params.DisclaimerVersion,
		SignedAt:          params.DisclaimerSignedAt,
		ClientIP:          params.ClientIP,
		UserAgent:         params.UserAgent,
		CreatedAt:         params.Now,
	}
	if err := tx.Create(signature).Error; err != nil {
		if isDuplicateKey(err) {
			return ErrDuplicate
		}
		return fmt.Errorf("create disclaimer signature: %w", err)
	}
	return nil
}

type lockState struct {
	Status    string `gorm:"column:status"`
	GroupID   string `gorm:"column:group_id"`
	IsExpired bool   `gorm:"column:is_expired"`
}

func classifyLockFailure(tx *gorm.DB, params CreateParams) error {
	var state lockState
	if err := tx.Raw(`
		SELECT status, group_id, (expires_at <= NOW(3)) AS is_expired
		  FROM enrollment_locks
		 WHERE token = ? AND user_id = ?
		 LIMIT 1
		 FOR UPDATE`,
		params.LockToken,
		params.UserID,
	).Scan(&state).Error; err != nil {
		return fmt.Errorf("read lock state: %w", err)
	}

	if state.Status == "" || state.GroupID != params.GroupID {
		return ErrLockInvalid
	}
	switch state.Status {
	case "consumed":
		return ErrLockAlreadyConsumed
	case "expired":
		return ErrLockExpired
	case "active":
		if state.IsExpired {
			return ErrLockExpired
		}
		return ErrLockInvalid
	default:
		return ErrLockInvalid
	}
}

func isDuplicateKey(err error) bool {
	var mysqlErr *gomysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
