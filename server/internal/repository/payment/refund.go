package payment

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"bike-enroll/server/internal/model"
)

const (
	refundStatusSuccess    = "SUCCESS"
	refundStatusProcessing = "PROCESSING"
)

var (
	// ErrRefundNotFound 表示退款记录不存在。
	ErrRefundNotFound = errors.New("refund not found")
	// ErrRefundConflict 表示 out_refund_no 已属于另一笔订单。
	ErrRefundConflict = errors.New("refund belongs to another order")
	// ErrRefundOrderStatus 表示订单当前状态不允许申请退款。
	ErrRefundOrderStatus = errors.New("order status not refundable")
)

// RefundParams 是退款回调落库参数。
type RefundParams struct {
	OutTradeNo   string
	OutRefundNo  string
	RefundID     string
	RefundStatus string
	RefundAmount int
	Now          time.Time
}

// StartRefundParams 是申请退款落库参数。
type StartRefundParams struct {
	OrderID     string
	OutRefundNo string
	RefundID    string
	Status      string
	Amount      int
	Now         time.Time
}

// RefundOutcome 是退款回调在数据库中的处理结果。
type RefundOutcome string

const (
	// RefundApplied 表示 Order/Enrollment 已迁移为 refunded，并完成 Redis 回补。
	RefundApplied RefundOutcome = "applied"
	// RefundAlreadyDone 表示 Order 已经是 refunded，幂等返回。
	RefundAlreadyDone RefundOutcome = "already_refunded"
	// RefundNotSuccess 表示退款回调不是 SUCCESS（CLOSED/ABNORMAL），只记录不迁移。
	RefundNotSuccess RefundOutcome = "not_success"
	// RefundIgnoredOrderStatus 表示 Order 状态不在 paid/refunding，不能退款。
	RefundIgnoredOrderStatus RefundOutcome = "ignored_order_status"
	// RefundUnknownOrder 表示回调里的 out_trade_no 不存在。
	RefundUnknownOrder RefundOutcome = "unknown_order"
)

// RefundRepository 是退款申请与退款回调的数据访问接口。
type RefundRepository interface {
	GetByOutRefundNo(ctx context.Context, outRefundNo string) (*model.Refund, error)
	GetLatestByOrder(ctx context.Context, orderID string) (*model.Refund, error)
	StartRefund(ctx context.Context, params StartRefundParams) (*model.Refund, error)
	ApplyRefundNotify(ctx context.Context, params RefundParams) (RefundOutcome, error)
}

// GORMRefundRepository 是 RefundRepository 的 MySQL + Redis 实现。
type GORMRefundRepository struct {
	db  *gorm.DB
	rdb *redis.Client
}

// NewGORMRefund 创建退款仓储。
func NewGORMRefund(db *gorm.DB, rdb *redis.Client) *GORMRefundRepository {
	return &GORMRefundRepository{db: db, rdb: rdb}
}

// GetByOutRefundNo 按商户退款单号查询退款记录。
func (r *GORMRefundRepository) GetByOutRefundNo(ctx context.Context, outRefundNo string) (*model.Refund, error) {
	var refund model.Refund
	if err := r.db.WithContext(ctx).
		Where("out_refund_no = ?", outRefundNo).
		First(&refund).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrRefundNotFound
		}
		return nil, fmt.Errorf("get refund by out_refund_no: %w", err)
	}
	return &refund, nil
}

// GetLatestByOrder 返回订单最新一笔退款记录。
func (r *GORMRefundRepository) GetLatestByOrder(ctx context.Context, orderID string) (*model.Refund, error) {
	var refund model.Refund
	if err := r.db.WithContext(ctx).
		Where("order_id = ?", orderID).
		Order("created_at DESC, id DESC").
		First(&refund).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrRefundNotFound
		}
		return nil, fmt.Errorf("get latest refund by order: %w", err)
	}
	return &refund, nil
}

// StartRefund 在事务内锁定 Order 并创建退款记录。
//
// out_refund_no 已存在时直接返回原记录，不创建新记录；本方法不操作 Redis。
func (r *GORMRefundRepository) StartRefund(ctx context.Context, params StartRefundParams) (*model.Refund, error) {
	var result *model.Refund
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var order model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", params.OrderID).
			First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrOrderNotFound
			}
			return fmt.Errorf("lock order for refund apply: %w", err)
		}

		var existing model.Refund
		err := tx.Where("out_refund_no = ?", params.OutRefundNo).First(&existing).Error
		switch {
		case err == nil:
			if existing.OrderID != order.ID {
				return ErrRefundConflict
			}
			result = &existing
			return nil
		case errors.Is(err, gorm.ErrRecordNotFound):
			// continue
		default:
			return fmt.Errorf("find refund by out_refund_no: %w", err)
		}

		if order.Status != "paid" {
			return ErrRefundOrderStatus
		}

		now := params.Now
		if now.IsZero() {
			now = time.Now()
		}
		status := params.Status
		if status == "" {
			status = refundStatusProcessing
		}
		var refundID *string
		if params.RefundID != "" {
			value := params.RefundID
			refundID = &value
		}
		refund := &model.Refund{
			ID:          uuid.NewString(),
			OrderID:     order.ID,
			OutRefundNo: params.OutRefundNo,
			RefundID:    refundID,
			Amount:      params.Amount,
			Status:      status,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := tx.Create(refund).Error; err != nil {
			return fmt.Errorf("create refund: %w", err)
		}

		orderUpdate := tx.Model(&model.Order{}).
			Where("id = ? AND status = ?", order.ID, "paid").
			Updates(map[string]any{
				"status":     "refunding",
				"updated_at": now,
			})
		if orderUpdate.Error != nil {
			return fmt.Errorf("mark order refunding: %w", orderUpdate.Error)
		}
		if orderUpdate.RowsAffected != 1 {
			return ErrRefundOrderStatus
		}

		result = refund
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ApplyRefundNotify 在单个事务内锁定 Order，并按退款状态决定是否迁移。
//
// 处理顺序：
//  1. SELECT orders ... FOR UPDATE；
//  2. order.status='refunded' 直接返回 RefundAlreadyDone（幂等）；
//  3. 仅 paid/refunding 继续；
//  4. RefundStatus=SUCCESS：Order/Enrollment→refunded，Redis INCR 回补；
//  5. 其他退款状态：不迁移、不回补，只更新退款记录状态。
func (r *GORMRefundRepository) ApplyRefundNotify(ctx context.Context, params RefundParams) (RefundOutcome, error) {
	outcome := RefundUnknownOrder
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var order model.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", params.OutTradeNo).
			First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				outcome = RefundUnknownOrder
				return nil
			}
			return fmt.Errorf("lock order for refund: %w", err)
		}

		var refund model.Refund
		refundFound := true
		if err := tx.Where("out_refund_no = ?", params.OutRefundNo).First(&refund).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				refundFound = false
			} else {
				return fmt.Errorf("find refund for notify: %w", err)
			}
		} else if refund.OrderID != order.ID {
			return ErrRefundConflict
		}

		now := params.Now
		if now.IsZero() {
			now = time.Now()
		}

		if order.Status == "refunded" {
			outcome = RefundAlreadyDone
			return nil
		}
		if order.Status != "paid" && order.Status != "refunding" {
			outcome = RefundIgnoredOrderStatus
			return nil
		}
		if params.RefundStatus != refundStatusSuccess {
			if refundFound {
				if err := tx.Model(&model.Refund{}).
					Where("id = ?", refund.ID).
					Updates(map[string]any{"status": params.RefundStatus, "updated_at": now}).Error; err != nil {
					return fmt.Errorf("mark refund not success: %w", err)
				}
			}
			// 仅已进入 refunding 的订单进入 refund_failed；paid 订单没有申请受理，
			// 乱序到达的 CLOSED/ABNORMAL 不改变主状态。
			if order.Status == "refunding" {
				failedUpdate := tx.Model(&model.Order{}).
					Where("id = ? AND status = ?", order.ID, "refunding").
					Updates(map[string]any{"status": "refund_failed", "updated_at": now})
				if failedUpdate.Error != nil {
					return fmt.Errorf("mark order refund_failed: %w", failedUpdate.Error)
				}
				if failedUpdate.RowsAffected != 1 {
					return errors.New("order state changed while locking for refund failure")
				}
			}
			outcome = RefundNotSuccess
			return nil
		}

		orderUpdate := tx.Model(&model.Order{}).
			Where("id = ? AND status IN ?", order.ID, []string{"paid", "refunding"}).
			Updates(map[string]any{
				"status":     "refunded",
				"updated_at": now,
			})
		if orderUpdate.Error != nil {
			return fmt.Errorf("mark order refunded: %w", orderUpdate.Error)
		}
		if orderUpdate.RowsAffected != 1 {
			outcome = RefundIgnoredOrderStatus
			return nil
		}

		enrollmentUpdate := tx.Model(&model.Enrollment{}).
			Where("order_id = ? AND status IN ?", order.ID, []string{"paid", "confirmed"}).
			Updates(map[string]any{
				"status":     "refunded",
				"updated_at": now,
			})
		if enrollmentUpdate.Error != nil {
			return fmt.Errorf("mark enrollment refunded: %w", enrollmentUpdate.Error)
		}
		if enrollmentUpdate.RowsAffected != 1 {
			return errors.New("refundable enrollment not found for order")
		}

		// 名额回补：只有条件更新成功（RowsAffected=1）才执行，保证恰好一次。
		if err := r.rdb.Incr(ctx, "group:"+order.GroupID+":remaining").Err(); err != nil {
			return fmt.Errorf("reclaim group slot for refund: %w", err)
		}

		if refundFound {
			if err := tx.Model(&model.Refund{}).
				Where("id = ?", refund.ID).
				Updates(map[string]any{"status": refundStatusSuccess, "updated_at": now}).Error; err != nil {
				return fmt.Errorf("mark refund success: %w", err)
			}
		}

		outcome = RefundApplied
		return nil
	})
	if err != nil {
		return "", err
	}
	return outcome, nil
}
