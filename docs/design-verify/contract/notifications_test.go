package contract

import (
	"net/http"
	"testing"
)

// TestContractNotificationSubscribe 覆盖 POST /notifications/subscribe。
func TestContractNotificationSubscribe(t *testing.T) {
	runPathContract(t, http.MethodPost, "/notifications/subscribe")
}
