// Package auth 实现微信小程序登录业务逻辑。
package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"bike-enroll/server/internal/model"
	"bike-enroll/server/internal/pkg/wechat"
	userrepo "bike-enroll/server/internal/repository/user"
)

// ErrInvalidArgument 表示登录参数非法。
var ErrInvalidArgument = errors.New("invalid argument")

// ErrInvalidCode 表示微信 code 无效或已过期。
var ErrInvalidCode = errors.New("invalid wechat code")

// ErrUpstream 表示微信接口或内部依赖异常。
var ErrUpstream = errors.New("wechat login upstream error")

// WeChatClient 是 code2session 的最小接口，便于测试替换。
type WeChatClient interface {
	Code2Session(ctx context.Context, code string) (wechat.Session, error)
}

// TokenIssuer 签发 JWT。
type TokenIssuer interface {
	Issue(userID, role string) (string, error)
}

// Service 聚合微信登录所需依赖。
type Service struct {
	users  userrepo.Repository
	wechat WeChatClient
	tokens TokenIssuer
}

// New 创建登录服务。
func New(users userrepo.Repository, wechatClient WeChatClient, tokens TokenIssuer) *Service {
	return &Service{
		users:  users,
		wechat: wechatClient,
		tokens: tokens,
	}
}

// LoginResult 是登录成功结果。
type LoginResult struct {
	Token string
	User  *model.User
}

// Login 使用 wx.login 的 code 换取 openid，创建或复用用户并签发 JWT。
func (s *Service) Login(ctx context.Context, code string) (*LoginResult, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, ErrInvalidArgument
	}
	if s.users == nil || s.wechat == nil || s.tokens == nil {
		return nil, fmt.Errorf("%w: missing dependency", ErrUpstream)
	}

	session, err := s.wechat.Code2Session(ctx, code)
	if err != nil {
		if errors.Is(err, wechat.ErrInvalidCode) {
			return nil, fmt.Errorf("%w: %v", ErrInvalidCode, err)
		}
		return nil, fmt.Errorf("%w: %v", ErrUpstream, err)
	}

	openID := strings.TrimSpace(session.OpenID)
	if openID == "" {
		return nil, fmt.Errorf("%w: empty openid", ErrUpstream)
	}

	user, err := s.users.FindByOpenID(ctx, openID)
	switch {
	case err == nil:
		// 老用户复用
	case errors.Is(err, userrepo.ErrNotFound):
		user, err = s.createUser(ctx, openID, session.UnionID)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("find user: %w", err)
	}

	role := user.Role
	if role == "" {
		role = "rider"
		user.Role = role
	}
	token, err := s.tokens.Issue(user.ID, role)
	if err != nil {
		return nil, fmt.Errorf("issue token: %w", err)
	}

	return &LoginResult{Token: token, User: user}, nil
}

func (s *Service) createUser(ctx context.Context, openID, unionID string) (*model.User, error) {
	now := time.Now()
	candidate := &model.User{
		ID:        uuid.NewString(),
		OpenID:    openID,
		UnionID:   unionID,
		Nickname:  "微信用户",
		AvatarURL: "",
		Role:      "rider",
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.users.Create(ctx, candidate); err != nil {
		// 并发首次登录可能同时创建；唯一键冲突时回读已存在用户。
		if errors.Is(err, userrepo.ErrDuplicate) {
			user, findErr := s.users.FindByOpenID(ctx, openID)
			if findErr != nil {
				return nil, fmt.Errorf("load user after duplicate: %w", findErr)
			}
			return user, nil
		}
		return nil, fmt.Errorf("create user: %w", err)
	}
	return candidate, nil
}
