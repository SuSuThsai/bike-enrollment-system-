package contract

import (
	"net/http"
	"testing"
)

// TestContractAuthWechatLogin 覆盖 POST /auth/wechat-login。
func TestContractAuthWechatLogin(t *testing.T) {
	runPathContract(t, http.MethodPost, "/auth/wechat-login")
}
