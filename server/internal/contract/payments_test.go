package contract

import (
	"net/http"
	"testing"
)

// TestContractPaymentPrepay 覆盖 POST /payments/prepay。
func TestContractPaymentPrepay(t *testing.T) {
	runPathContract(t, http.MethodPost, "/payments/prepay")
}

// TestContractPaymentNotify 覆盖 POST /payments/notify。
//
// 成功回调依赖微信 V3 签名，见 contractPaths 中的 SkipValidReason。
func TestContractPaymentNotify(t *testing.T) {
	runPathContract(t, http.MethodPost, "/payments/notify")
}
