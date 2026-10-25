// Package payment 提供支付预下单所需的数据访问。
package payment

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"bike-enroll/server/internal/model"
)

// ErrOrderNotFound 表示订单不存在或不属于当前用户。
var ErrOrderNotFound = errors.New("order not found")

// NotifyOutcome 是支付回调在数据库中的处理结果。
type NotifyOutcome string

const (
	NotifyApplied        NotifyOutcome = "applied"
	NotifyDuplicate      NotifyOutcome = "duplicate"
	NotifyAmountMismatch NotifyOutcome = "amount_mismatch"
	NotifyUnknownOrder   NotifyOutcome = "unknown_order"
)

// NotifyParams 是支付回调落库参数。
type NotifyParams struct {
	OutTradeNo    string
	TransactionID string
	AmountTotal   int
	Now           time.Time
}

var errRollbackNotify = errors.New("rollback payment notify")

// Repository 是支付预下单与回调数据访问接口。
type Repository interface {
	GetOrder(ctx context.Context, orderID string) (*model.Order, error)
	GetOrderWithUser(ctx context.Context, orderID string) (*model.Order, *model.User, error)
	ApplyNotify(ctx context.Context, params NotifyParams) (NotifyOutcome, error)
}

// GORMRepository 是 Repository 的 MySQL/GORM 实现。
type GORMRepository struct {
	db *gorm.DB
}

// NewGORM 创建支付仓储。
func NewGORM(db *gorm.DB) *GORMRepository {
	return &GORMRepository{db: db}
}

// GetOrder 返回订单。
func (r *GORMRepository) GetOrder(ctx context.Context, orderID string) (*model.Order, error) {
	var order model.Order
	if err := r.db.WithContext(ctx).Where("id = ?", orderID).First(&order).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("find order: %w", err)
	}
	return &order, nil
}

// GetOrderWithUser 返回订单与下单用户（含 openid）。
func (r *GORMRepository) GetOrderWithUser(ctx context.Context, orderID string) (*model.Order, *model.User, error) {
	var order model.Order
	if err := r.db.WithContext(ctx).Where("id = ?", orderID).First(&order).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrOrderNotFound
		}
		return nil, nil, fmt.Errorf("find order: %w", err)
	}

	var user model.User
	if err := r.db.WithContext(ctx).Where("id = ?", order.UserID).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrOrderNotFound
		}
		return nil, nil, fmt.Errorf("find order user: %w", err)
	}
	return &order, &user, nil
}

// ApplyNotify 在单个事务内锁定 Order，并按状态/金额决定是否迁移。
//
// 未知订单与金额不符会回滚且不修改数据；重复回调直接提交且不更新。
func (r *GORMRepository) ApplyNotify(ctx context.Context, params NotifyParams) (NotifyOutcome, error) {
	outcome := NotifyUnknownOrder
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var order model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", params.OutTradeNo).
			First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errRollbackNotify
			}
			return fmt.Errorf("lock order: %w", err)
		}

		if order.Status != "pending_payment" {
			outcome = NotifyDuplicate
			return nil
		}
		if params.AmountTotal != order.Amount {
			outcome = NotifyAmountMismatch
			return errRollbackNotify
		}

		orderUpdate := tx.Model(&model.Order{}).
			Where("id = ? AND status = ?", order.ID, "pending_payment").
			Updates(map[string]any{
				"status":         "paid",
				"paid_at":        params.Now,
				"transaction_id": params.TransactionID,
				"updated_at":     params.Now,
			})
		if orderUpdate.Error != nil {
			return fmt.Errorf("mark order paid: %w", orderUpdate.Error)
		}
		if orderUpdate.RowsAffected != 1 {
			return errors.New("order state changed while locked")
		}

		enrollmentUpdate := tx.Model(&model.Enrollment{}).
			Where("order_id = ? AND status = ?", order.ID, "pending_payment").
			Updates(map[string]any{
				"status":     "paid",
				"paid_at":    params.Now,
				"updated_at": params.Now,
			})
		if enrollmentUpdate.Error != nil {
			return fmt.Errorf("mark enrollment paid: %w", enrollmentUpdate.Error)
		}
		if enrollmentUpdate.RowsAffected != 1 {
			return errors.New("pending enrollment not found for order")
		}

		outcome = NotifyApplied
		return nil
	})
	if errors.Is(err, errRollbackNotify) {
		return outcome, nil
	}
	if err != nil {
		return "", err
	}
	return outcome, nil
}
