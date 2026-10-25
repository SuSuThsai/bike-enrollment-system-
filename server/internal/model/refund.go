package model

import "time"

// Refund 是退款记录。
//
// out_refund_no 唯一，用于重复 apply 的幂等；refund_id 为支付平台退款单号。
type Refund struct {
	ID          string    `gorm:"column:id;type:char(36);primaryKey"`
	OrderID     string    `gorm:"column:order_id;type:char(36);not null;index:idx_refunds_order"`
	OutRefundNo string    `gorm:"column:out_refund_no;type:varchar(64);not null;uniqueIndex:uk_refunds_out_refund_no"`
	RefundID    *string   `gorm:"column:refund_id;type:varchar(64);uniqueIndex:uk_refunds_refund_id"`
	Amount      int       `gorm:"column:amount;type:int;not null"`
	Status      string    `gorm:"column:status;type:varchar(16);not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null"`
	UpdatedAt   time.Time `gorm:"column:updated_at;not null"`
}

// TableName 固定表名。
func (Refund) TableName() string {
	return "refunds"
}
