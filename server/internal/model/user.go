// Package model 定义 GORM 持久化模型。
package model

import "time"

// User 是微信小程序用户。
//
// 不在此模型上挂 JSON tag，避免把 openid 等内部字段意外返回给客户端。
type User struct {
	ID        string    `gorm:"column:id;type:char(36);primaryKey"`
	OpenID    string    `gorm:"column:openid;type:varchar(64);not null;uniqueIndex:uk_users_openid"`
	UnionID   string    `gorm:"column:unionid;type:varchar(64);not null;default:'';index:idx_users_unionid"`
	Nickname  string    `gorm:"column:nickname;type:varchar(64);not null;default:''"`
	AvatarURL string    `gorm:"column:avatar_url;type:varchar(512);not null;default:''"`
	Role      string    `gorm:"column:role;type:varchar(16);not null;default:rider"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

// TableName 固定表名，避免 GORM 复数化。
func (User) TableName() string {
	return "users"
}
