package payment

import (
	"fmt"
	"log/slog"
	"strings"
)

// 支持的支付模式。
const (
	ModeMock   = "mock"
	ModeWeChat = "wechat"
)

// Config 是支付提供者工厂配置。
type Config struct {
	// Mode 对应环境变量 PAYMENT_MODE：mock / wechat。
	Mode string
	// AppEnv 对应环境变量 APP_ENV；production/prod 下禁止 mock。
	AppEnv string
	WeChat WeChatConfig
}

// NewProvider 根据 PAYMENT_MODE 返回对应 Provider。
//
// 空值按 wechat 处理，避免生产环境因漏配环境变量而静默降级为 mock。
// APP_ENV=production/prod 与 PAYMENT_MODE=mock 同时出现时返回错误，由进程启动方拒绝启动。
func NewProvider(cfg Config, logger *slog.Logger) (Provider, error) {
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode == "" {
		mode = ModeWeChat
	}
	if mode == ModeMock && isProductionEnv(cfg.AppEnv) {
		return nil, fmt.Errorf("PAYMENT_MODE=mock is forbidden when APP_ENV=%q", cfg.AppEnv)
	}

	switch mode {
	case ModeMock:
		if logger != nil {
			logger.Warn("payment provider running in MOCK mode; notify signatures are NOT verified")
		}
		return NewMockProvider(), nil
	case ModeWeChat:
		if logger != nil {
			logger.Info("payment provider initialized", "mode", mode)
		}
		return NewWeChatProvider(cfg.WeChat, logger), nil
	default:
		return nil, fmt.Errorf("unsupported PAYMENT_MODE %q (want %q or %q)", cfg.Mode, ModeMock, ModeWeChat)
	}
}

func isProductionEnv(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "production", "prod":
		return true
	default:
		return false
	}
}
