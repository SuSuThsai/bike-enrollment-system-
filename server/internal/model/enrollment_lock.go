// Package model 定义 GORM 持久化模型。
package model

import "time"

// EnrollmentLock 是名额锁定记录，状态权威存储在 MySQL。
type EnrollmentLock struct {
	Token      string     `gorm:"column:token;type:char(36);primaryKey"`
	UserID     string     `gorm:"column:user_id;type:char(36);not null;index:idx_locks_user_group,priority:1"`
	GroupID    string     `gorm:"column:group_id;type:char(36);not null;index:idx_locks_user_group,priority:2"`
	Status     string     `gorm:"column:status;type:varchar(16);not null;index:idx_locks_status_expires,priority:1"`
	ExpiresAt  time.Time  `gorm:"column:expires_at;not null;index:idx_locks_status_expires,priority:2"`
	ConsumedAt *time.Time `gorm:"column:consumed_at"`
	ExpiredAt  *time.Time `gorm:"column:expired_at"`
	CreatedAt  time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt  time.Time  `gorm:"column:updated_at;not null"`
}

// TableName 固定表名。
func (EnrollmentLock) TableName() string {
	return "enrollment_locks"
}
