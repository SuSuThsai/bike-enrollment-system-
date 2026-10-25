// Package model 定义 GORM 持久化模型。
package model

import "time"

// Enrollment 是报名记录，状态权威存储在 MySQL。
type Enrollment struct {
	ID          string         `gorm:"column:id;type:char(36);primaryKey"`
	UserID      string         `gorm:"column:user_id;type:char(36);not null;uniqueIndex:uk_enrollments_user_group,priority:1;index:idx_enrollments_user_created,priority:1"`
	EventID     string         `gorm:"column:event_id;type:char(36);not null"`
	GroupID     string         `gorm:"column:group_id;type:char(36);not null;uniqueIndex:uk_enrollments_user_group,priority:2;index:idx_enrollments_group_status,priority:1"`
	OrderID     string         `gorm:"column:order_id;type:char(36);not null"`
	Status      string         `gorm:"column:status;type:varchar(16);not null;index:idx_enrollments_group_status,priority:2"`
	FormData    map[string]any `gorm:"column:form_data;type:json;not null;serializer:json"`
	PaidAt      *time.Time     `gorm:"column:paid_at"`
	CancelledAt *time.Time     `gorm:"column:cancelled_at"`
	CreatedAt   time.Time      `gorm:"column:created_at;not null;index:idx_enrollments_user_created,priority:2"`
	UpdatedAt   time.Time      `gorm:"column:updated_at;not null"`
}

// TableName 固定表名。
func (Enrollment) TableName() string {
	return "enrollments"
}

// Order 是报名订单，金额单位为分。
type Order struct {
	ID            string     `gorm:"column:id;type:char(36);primaryKey"`
	EnrollmentID  string     `gorm:"column:enrollment_id;type:char(36);not null;uniqueIndex:uk_orders_enrollment"`
	UserID        string     `gorm:"column:user_id;type:char(36);not null;index:idx_orders_user_created,priority:1"`
	GroupID       string     `gorm:"column:group_id;type:char(36);not null"`
	Amount        int        `gorm:"column:amount;type:int;not null"`
	Status        string     `gorm:"column:status;type:varchar(16);not null;index:idx_orders_status_created,priority:1"`
	TransactionID *string    `gorm:"column:transaction_id;type:varchar(64);uniqueIndex:uk_orders_transaction_id"`
	PaidAt        *time.Time `gorm:"column:paid_at"`
	ExpiredAt     *time.Time `gorm:"column:expired_at"`
	CreatedAt     time.Time  `gorm:"column:created_at;not null;index:idx_orders_status_created,priority:2;index:idx_orders_user_created,priority:2"`
	UpdatedAt     time.Time  `gorm:"column:updated_at;not null"`
}

// TableName 固定表名。
func (Order) TableName() string {
	return "orders"
}

// DisclaimerSignature 是免责协议签署记录，只允许 INSERT。
type DisclaimerSignature struct {
	ID                string    `gorm:"column:id;type:char(36);primaryKey"`
	UserID            string    `gorm:"column:user_id;type:char(36);not null"`
	EnrollmentID      string    `gorm:"column:enrollment_id;type:char(36);not null;uniqueIndex:uk_disclaimer_enrollment"`
	GroupID           string    `gorm:"column:group_id;type:char(36);not null"`
	DisclaimerVersion string    `gorm:"column:disclaimer_version;type:varchar(64);not null"`
	SignedAt          time.Time `gorm:"column:signed_at;not null"`
	ClientIP          string    `gorm:"column:client_ip;type:varchar(45);not null"`
	UserAgent         string    `gorm:"column:user_agent;type:varchar(512);not null"`
	CreatedAt         time.Time `gorm:"column:created_at;not null"`
}

// TableName 固定表名。
func (DisclaimerSignature) TableName() string {
	return "disclaimer_signatures"
}
