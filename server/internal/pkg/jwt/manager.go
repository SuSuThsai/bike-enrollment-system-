// Package jwt 负责 JWT 的签发与校验。
package jwt

import (
	"errors"
	"fmt"
	"strings"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
)

const issuer = "bike-enroll-api"

// ErrInvalidToken 表示 token 缺失、签名不正确或格式非法。
var ErrInvalidToken = errors.New("invalid token")

// ErrExpired 表示 token 已过期。
var ErrExpired = errors.New("token expired")

// Claims 是 API JWT 的声明集合。
type Claims struct {
	Role string `json:"role"`
	jwtlib.RegisteredClaims
}

// Manager 使用 HS256 签发和校验 JWT。
type Manager struct {
	secret []byte
	expire time.Duration
}

// NewManager 创建 JWT 管理器。
//
// JWT_SECRET 必须至少 32 字节（docs/project-context.md），且不得是曾出现在
// 示例配置中的已知默认值。
func NewManager(secret string, expire time.Duration) (*Manager, error) {
	if len(secret) < 32 {
		return nil, errors.New("JWT_SECRET must be at least 32 bytes")
	}
	if isKnownWeakJWTSecret(secret) {
		return nil, errors.New("JWT_SECRET must not be a published default; generate one with: openssl rand -hex 32")
	}
	if expire <= 0 {
		return nil, errors.New("JWT expire duration must be positive")
	}
	return &Manager{secret: []byte(secret), expire: expire}, nil
}

// isKnownWeakJWTSecret 判断密钥是否为曾经公开过的默认值。
//
// 这些字符串长期作为 docker-compose.yml 与 .env.example 的默认值公开存在，
// 等同已泄露：只要长度达标就能通过校验，因此必须在启动时显式拒绝。
func isKnownWeakJWTSecret(secret string) bool {
	switch secret {
	case "dev-only-change-me-32-bytes-minimum", "change-me":
		return true
	default:
		return false
	}
}

// Issue 为指定用户签发 JWT。
func (m *Manager) Issue(userID, role string) (string, error) {
	if strings.TrimSpace(userID) == "" {
		return "", errors.New("user id is required")
	}
	if role == "" {
		role = "rider"
	}

	now := time.Now()
	claims := &Claims{
		Role: role,
		RegisteredClaims: jwtlib.RegisteredClaims{
			Subject:   userID,
			Issuer:    issuer,
			IssuedAt:  jwtlib.NewNumericDate(now),
			ExpiresAt: jwtlib.NewNumericDate(now.Add(m.expire)),
		},
	}

	token := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secret)
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return signed, nil
}

// Parse 校验 token 并返回声明。
func (m *Manager) Parse(raw string) (*Claims, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrInvalidToken
	}

	claims := &Claims{}
	token, err := jwtlib.ParseWithClaims(
		raw,
		claims,
		func(token *jwtlib.Token) (any, error) {
			if token.Method != jwtlib.SigningMethodHS256 {
				return nil, ErrInvalidToken
			}
			return m.secret, nil
		},
		jwtlib.WithValidMethods([]string{jwtlib.SigningMethodHS256.Alg()}),
	)
	if err != nil {
		if errors.Is(err, jwtlib.ErrTokenExpired) {
			return nil, ErrExpired
		}
		return nil, ErrInvalidToken
	}
	if !token.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
