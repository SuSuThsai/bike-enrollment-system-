// Package model 定义 GORM 持久化模型。
package model

import "time"

// 赛事生命周期状态（events.lifecycle_status）。
const (
	LifecycleDraft     = "draft"
	LifecyclePublished = "published"
	LifecycleFinished  = "finished"
	LifecycleCancelled = "cancelled"
)

// Event 是赛事。
//
// lifecycle_status 只表达赛事生命周期；报名是否开放由 resolveRegistrationStatus
// 结合 registration_* 字段实时推导，不落库。
type Event struct {
	ID        string    `gorm:"column:id;type:char(36);primaryKey"`
	Title     string    `gorm:"column:title;type:varchar(200);not null"`
	CoverURL  string    `gorm:"column:cover_url;type:varchar(512);not null;default:''"`
	StartTime time.Time `gorm:"column:start_time;not null;index:idx_events_lifecycle_start,priority:2"`
	EndTime   time.Time `gorm:"column:end_time;not null"`
	Location  string    `gorm:"column:location;type:varchar(255);not null"`

	// LifecycleStatus 取值见 Lifecycle* 常量。
	LifecycleStatus string `gorm:"column:lifecycle_status;type:varchar(32);not null;default:draft;index:idx_events_lifecycle_start,priority:1"`

	// RegistrationOpenAt / RegistrationDeadline 为 NULL 表示该侧不限制。
	RegistrationOpenAt   *time.Time `gorm:"column:registration_open_at"`
	RegistrationDeadline *time.Time `gorm:"column:registration_deadline"`

	// RegistrationManuallyClosed 为 true 时报名状态恒为 closed。
	RegistrationManuallyClosed bool `gorm:"column:registration_manually_closed;not null;default:false"`
	// RegistrationClosedReason 是手动关闭原因（可空）。
	RegistrationClosedReason *string `gorm:"column:registration_closed_reason;type:varchar(255)"`

	Description string    `gorm:"column:description;type:text;not null"`
	Rules       string    `gorm:"column:rules;type:text;not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null"`
	UpdatedAt   time.Time `gorm:"column:updated_at;not null"`
}

// TableName 固定表名。
func (Event) TableName() string {
	return "events"
}

// Group 是赛事组别。
//
// status 只落库 open / closed；sold_out 在读取时根据 Redis remaining 推导。
type Group struct {
	ID                string         `gorm:"column:id;type:char(36);primaryKey"`
	EventID           string         `gorm:"column:event_id;type:char(36);not null;index:idx_groups_event_start,priority:1"`
	Name              string         `gorm:"column:name;type:varchar(64);not null"`
	Price             int            `gorm:"column:price;type:int;not null"`
	TotalSlots        int            `gorm:"column:total_slots;type:int;not null"`
	StartTime         time.Time      `gorm:"column:start_time;not null;index:idx_groups_event_start,priority:2"`
	Status            string         `gorm:"column:status;type:varchar(16);not null;default:open"`
	FormSchema        map[string]any `gorm:"column:form_schema;type:json;not null;serializer:json"`
	DisclaimerVersion string         `gorm:"column:disclaimer_version;type:varchar(64);not null"`
	CreatedAt         time.Time      `gorm:"column:created_at;not null"`
	UpdatedAt         time.Time      `gorm:"column:updated_at;not null"`
}

// TableName 固定表名。
func (Group) TableName() string {
	return "groups"
}
