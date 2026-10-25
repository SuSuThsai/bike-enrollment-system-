// Package notification 提供订阅授权与通知数据访问。
package notification

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"bike-enroll/server/internal/model"
)

// ErrNotificationNotFound 表示无法找到通知所需的订单/报名/用户信息。
var ErrNotificationNotFound = errors.New("notification data not found")

// EnrollmentNotification 是报名成功通知所需的数据。
type EnrollmentNotification struct {
	UserID     string
	OpenID     string
	EventTitle string
	GroupName  string
	OrderID    string
	Amount     int
	PaidAt     *time.Time
}

// Repository 是订阅授权与通知数据访问接口。
type Repository interface {
	Subscribe(ctx context.Context, userID string, templateIDs []string) error
	ConsumeAuthorization(ctx context.Context, userID, templateID string) (bool, error)
	GetEnrollmentNotification(ctx context.Context, orderID string) (*EnrollmentNotification, error)
}

// GORMRepository 是 Repository 的 MySQL/GORM 实现。
type GORMRepository struct {
	db *gorm.DB
}

// NewGORM 创建通知仓储。
func NewGORM(db *gorm.DB) *GORMRepository {
	return &GORMRepository{db: db}
}

// Subscribe 为每个模板记录一条 available 授权。
func (r *GORMRepository) Subscribe(ctx context.Context, userID string, templateIDs []string) error {
	if len(templateIDs) == 0 {
		return nil
	}
	now := time.Now()
	rows := make([]model.NotificationAuthorization, 0, len(templateIDs))
	for _, templateID := range templateIDs {
		rows = append(rows, model.NotificationAuthorization{
			ID:         uuid.NewString(),
			UserID:     userID,
			TemplateID: templateID,
			Status:     "available",
			CreatedAt:  now,
		})
	}
	if err := r.db.WithContext(ctx).Create(&rows).Error; err != nil {
		return fmt.Errorf("create notification authorizations: %w", err)
	}
	return nil
}

// ConsumeAuthorization 原子消费一条未使用的模板授权。
func (r *GORMRepository) ConsumeAuthorization(ctx context.Context, userID, templateID string) (bool, error) {
	result := r.db.WithContext(ctx).Exec(`
		UPDATE notification_authorizations
		   SET status = 'consumed', consumed_at = NOW(3)
		 WHERE user_id = ? AND template_id = ? AND status = 'available'
		 ORDER BY created_at
		 LIMIT 1`,
		userID,
		templateID,
	)
	if result.Error != nil {
		return false, fmt.Errorf("consume notification authorization: %w", result.Error)
	}
	return result.RowsAffected == 1, nil
}

// GetEnrollmentNotification 返回报名成功通知所需的用户/赛事/组别信息。
func (r *GORMRepository) GetEnrollmentNotification(ctx context.Context, orderID string) (*EnrollmentNotification, error) {
	var item EnrollmentNotification
	if err := r.db.WithContext(ctx).Raw(`
		SELECT o.user_id AS user_id, u.openid AS open_id, e.title AS event_title,
		       g.name AS group_name, o.id AS order_id, o.amount AS amount, o.paid_at AS paid_at
		  FROM orders o
		  JOIN enrollments en ON en.order_id = o.id
		  JOIN users u ON u.id = o.user_id
		  JOIN events e ON e.id = en.event_id
		  JOIN `+"`groups`"+` g ON g.id = en.group_id
		 WHERE o.id = ?`,
		orderID,
	).Scan(&item).Error; err != nil {
		return nil, fmt.Errorf("load enrollment notification: %w", err)
	}
	if item.UserID == "" {
		return nil, ErrNotificationNotFound
	}
	return &item, nil
}
