// Package user 提供用户数据的持久化访问。
package user

import (
	"context"
	"errors"
	"fmt"

	gomysql "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"

	"bike-enroll/server/internal/model"
)

// ErrNotFound 表示用户不存在。
var ErrNotFound = errors.New("user not found")

// ErrDuplicate 表示 openid 唯一键冲突。
var ErrDuplicate = errors.New("user already exists")

// Repository 是用户仓储接口；service 依赖该接口，便于单元测试替换。
type Repository interface {
	FindByOpenID(ctx context.Context, openid string) (*model.User, error)
	Create(ctx context.Context, user *model.User) error
}

// GORMRepository 是 Repository 的 MySQL/GORM 实现。
type GORMRepository struct {
	db *gorm.DB
}

// NewGORM 创建基于 GORM 的用户仓储。
func NewGORM(db *gorm.DB) *GORMRepository {
	return &GORMRepository{db: db}
}

// FindByOpenID 按微信 openid 查询用户。
func (r *GORMRepository) FindByOpenID(ctx context.Context, openid string) (*model.User, error) {
	var user model.User
	err := r.db.WithContext(ctx).Where("openid = ?", openid).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find user by openid: %w", err)
	}
	return &user, nil
}

// Create 插入新用户；openid 唯一键冲突时返回 ErrDuplicate。
func (r *GORMRepository) Create(ctx context.Context, user *model.User) error {
	err := r.db.WithContext(ctx).Create(user).Error
	if err == nil {
		return nil
	}

	var mysqlErr *gomysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return ErrDuplicate
	}
	return fmt.Errorf("create user: %w", err)
}
