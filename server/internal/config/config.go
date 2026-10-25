// Package config 从环境变量加载服务配置。
//
// 变量清单以 docs/project-context.md 第四节为准。
package config

import (
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	gomysql "github.com/go-sql-driver/mysql"
)

// Config 是 API 进程的运行时配置。
type Config struct {
	Env      string
	HTTPAddr string
	LogLevel slog.Level

	MySQL MySQLConfig
	Redis RedisConfig
	JWT   JWTConfig

	LockTTLMinutes    int
	LockTTLSeconds    int
	PayTimeoutMinutes int
	PaymentMode       string
	MockPaySecret     string
	WeChat            WeChatConfig
	WeChatPay         WeChatPayConfig
}

// LockTTLDuration 返回锁有效期。
//
// LOCK_TTL_SECONDS 优先（压测时把 TTL 压缩到秒级），否则回落到 LOCK_TTL_MINUTES。
func (c Config) LockTTLDuration() time.Duration {
	if c.LockTTLSeconds > 0 {
		return time.Duration(c.LockTTLSeconds) * time.Second
	}
	return time.Duration(c.LockTTLMinutes) * time.Minute
}

// MySQLConfig 是 MySQL 连接配置。
type MySQLConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	Database string
}

// DSN 生成 go-sql-driver 连接串，避免手工拼接密码中的特殊字符。
func (c MySQLConfig) DSN() string {
	cfg := gomysql.NewConfig()
	cfg.User = c.User
	cfg.Passwd = c.Password
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(c.Host, c.Port)
	cfg.DBName = c.Database
	cfg.ParseTime = true
	cfg.Loc = time.Local
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 5 * time.Second
	cfg.WriteTimeout = 5 * time.Second
	cfg.Params = map[string]string{
		"charset":   "utf8mb4",
		"collation": "utf8mb4_0900_ai_ci",
	}
	return cfg.FormatDSN()
}

// RedisConfig 是 Redis 连接配置。
type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

// JWTConfig 是 JWT 签发配置。
type JWTConfig struct {
	Secret      string
	ExpireHours int
}

// ExpireDuration 返回 JWT 有效期；非法配置返回 0，由 jwt.Manager 拒绝。
func (c JWTConfig) ExpireDuration() time.Duration {
	if c.ExpireHours <= 0 {
		return 0
	}
	return time.Duration(c.ExpireHours) * time.Hour
}

// WeChatConfig 是微信小程序登录与订阅消息配置。
type WeChatConfig struct {
	AppID                string
	Secret               string
	TemplatePayResult    string
	TemplateEnrollResult string
}

// WeChatPayConfig 是微信支付 V3 配置。
type WeChatPayConfig struct {
	MchID            string
	SerialNo         string
	PrivateKeyPath   string
	NotifyURL        string
	APIv3Key         string
	PlatformCertPath string
}

// Load 读取环境变量。
//
// 非敏感项保留本地开发默认值；密码、JWT_SECRET、微信密钥等敏感项一律没有默认值，
// 缺失时返回空串，由调用方在启动期校验并拒绝启动，避免静默使用弱口令。
func Load() Config {
	return Config{
		Env:      getenv("APP_ENV", "development"),
		HTTPAddr: getenv("HTTP_ADDR", ":8080"),
		LogLevel: parseLogLevel(getenv("LOG_LEVEL", "info")),
		MySQL: MySQLConfig{
			Host:     getenv("MYSQL_HOST", "127.0.0.1"),
			Port:     getenv("MYSQL_PORT", "3306"),
			User:     getenv("MYSQL_USER", "bike"),
			Password: getenv("MYSQL_PASSWORD", ""),
			Database: getenv("MYSQL_DATABASE", "bike_enroll"),
		},
		Redis: RedisConfig{
			Addr:     getenv("REDIS_ADDR", "127.0.0.1:6379"),
			Password: getenv("REDIS_PASSWORD", ""),
			DB:       getenvInt("REDIS_DB", 0),
		},
		JWT: JWTConfig{
			Secret:      getenv("JWT_SECRET", ""),
			ExpireHours: getenvInt("JWT_EXPIRE_HOURS", 168),
		},
		LockTTLMinutes:    getenvInt("LOCK_TTL_MINUTES", 15),
		LockTTLSeconds:    getenvInt("LOCK_TTL_SECONDS", 0),
		PayTimeoutMinutes: getenvInt("PAY_TIMEOUT_MINUTES", 15),
		PaymentMode:       strings.ToLower(getenv("PAYMENT_MODE", "wechat")),
		MockPaySecret:     getenv("MOCK_PAY_SECRET", ""),
		WeChat: WeChatConfig{
			AppID:                getenv("WECHAT_APPID", ""),
			Secret:               getenv("WECHAT_SECRET", ""),
			TemplatePayResult:    getenv("WECHAT_TPL_PAY_RESULT", ""),
			TemplateEnrollResult: getenv("WECHAT_TPL_ENROLL_RESULT", ""),
		},
		WeChatPay: WeChatPayConfig{
			MchID:            getenv("WECHAT_PAY_MCHID", ""),
			SerialNo:         getenv("WECHAT_PAY_SERIAL_NO", ""),
			PrivateKeyPath:   getenv("WECHAT_PAY_PRIVATE_KEY_PATH", "./configs/apiclient_key.pem"),
			NotifyURL:        getenv("WECHAT_PAY_NOTIFY_URL", ""),
			APIv3Key:         getenvAny([]string{"WECHAT_PAY_APIV3_KEY", "WECHAT_PAY_API_V3_KEY"}, ""),
			PlatformCertPath: getenv("WECHAT_PAY_PLATFORM_CERT_PATH", "./configs/wechatpay_platform_cert.pem"),
		},
	}
}

func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// getenvAny 按顺序返回第一个非空的环境变量，用于兼容变量改名后的旧名称。
//
// 例：WECHAT_PAY_APIV3_KEY 为 .env.example 中的规范名称，
// WECHAT_PAY_API_V3_KEY 为历史名称，保留兼容以免既有部署失效。
func getenvAny(keys []string, fallback string) string {
	for _, key := range keys {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

func parseLogLevel(raw string) slog.Level {
	var level slog.Level
	if err := level.UnmarshalText([]byte(raw)); err != nil {
		return slog.LevelInfo
	}
	return level
}
