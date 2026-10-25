// Package model 定义 GORM 持久化模型。
package model

import "time"

// NotificationAuthorization 是用户对订阅消息模板的一次性授权记录。
type NotificationAuthorization struct {
	ID         string     `gorm:"column:id;type:char(36);primaryKey"`
	UserID     string     `gorm:"column:user_id;type:char(36);not null;index:idx_notification_auth_lookup,priority:1"`
	TemplateID string     `gorm:"column:template_id;type:varchar(64);not null;index:idx_notification_auth_lookup,priority:2"`
	Status     string     `gorm:"column:status;type:varchar(16);not null;default:available;index:idx_notification_auth_lookup,priority:3"`
	CreatedAt  time.Time  `gorm:"column:created_at;not null;index:idx_notification_auth_lookup,priority:4"`
	ConsumedAt *time.Time `gorm:"column:consumed_at"`
}

// TableName 固定表名。
func (NotificationAuthorization) TableName() string {
	return "notification_authorizations"
}
