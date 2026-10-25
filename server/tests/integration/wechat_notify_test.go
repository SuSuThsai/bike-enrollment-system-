//go:build integration

package integration_test

import (
	"bytes"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	paymenthandler "bike-enroll/server/internal/handler/payment"
	paymentpkg "bike-enroll/server/internal/pkg/payment"
	paymentrepo "bike-enroll/server/internal/repository/payment"
	paymentsvc "bike-enroll/server/internal/service/payment"
)

const wechatTestAPIv3Key = "0123456789abcdef0123456789abcdef"

type wechatNotifySigner struct {
	key      *rsa.PrivateKey
	serial   string
	apiV3Key string
}

// newRealWeChatProviderForTest 生成临时平台证书与 APIv3 密钥，构造真实 WeChatProvider。
func newRealWeChatProviderForTest(t *testing.T) (paymentpkg.Provider, *wechatNotifySigner) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成平台私钥失败: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(0x1234abcd),
		Subject:      pkix.Name{CommonName: "wechatpay-integration-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("生成平台证书失败: %v", err)
	}
	certPath := filepath.Join(t.TempDir(), "wechatpay_platform_cert.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("写入平台证书失败: %v", err)
	}

	provider := paymentpkg.NewWeChatProvider(paymentpkg.WeChatConfig{
		PlatformCertPath: certPath,
		APIv3Key:         wechatTestAPIv3Key,
	}, testLogger())

	return provider, &wechatNotifySigner{
		key:      key,
		serial:   strings.ToUpper(template.SerialNumber.Text(16)),
		apiV3Key: wechatTestAPIv3Key,
	}
}

func (s *wechatNotifySigner) buildNotify(t *testing.T, orderID string, amount int) ([]byte, http.Header) {
	t.Helper()

	transaction := map[string]any{
		"out_trade_no":   orderID,
		"transaction_id": "wx-real-" + uuid.NewString(),
		"trade_state":    "SUCCESS",
		"success_time":   time.Now().Format(time.RFC3339),
		"amount": map[string]any{
			"total":    amount,
			"currency": "CNY",
		},
	}
	plaintext, err := json.Marshal(transaction)
	if err != nil {
		t.Fatalf("序列化交易失败: %v", err)
	}

	nonce := "123456789012"
	associatedData := "transaction"
	block, err := aes.NewCipher([]byte(s.apiV3Key))
	if err != nil {
		t.Fatalf("创建 AES 失败: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("创建 GCM 失败: %v", err)
	}
	ciphertext := gcm.Seal(nil, []byte(nonce), plaintext, []byte(associatedData))

	envelope := map[string]any{
		"id":         "notify-" + uuid.NewString(),
		"event_type": "TRANSACTION.SUCCESS",
		"resource": map[string]any{
			"algorithm":       "AEAD_AES_256_GCM",
			"ciphertext":      base64.StdEncoding.EncodeToString(ciphertext),
			"associated_data": associatedData,
			"nonce":           nonce,
		},
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("序列化回调信封失败: %v", err)
	}

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signatureNonce := "notify-nonce"
	message := timestamp + "\n" + signatureNonce + "\n" + string(body) + "\n"
	digest := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("签名回调失败: %v", err)
	}

	headers := http.Header{}
	headers.Set("Wechatpay-Timestamp", timestamp)
	headers.Set("Wechatpay-Nonce", signatureNonce)
	headers.Set("Wechatpay-Signature", base64.StdEncoding.EncodeToString(signature))
	headers.Set("Wechatpay-Serial", s.serial)
	return body, headers
}

func postWeChatNotify(engine http.Handler, body []byte, headers http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/payments/notify", bytes.NewReader(body))
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func assertNotifyError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d，期望 %d: %s", rec.Code, status, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body["code"] != code {
		t.Errorf("响应 code = %q，期望 %q", body["code"], code)
	}
}

// TestIntegrationWeChatNotifyHTTP 使用真实 WeChatProvider（临时平台证书 + APIv3 密钥）
// 走完整 HTTP handler：验签失败 401 不改库；合法签名 200 完成支付；重复回调幂等。
func TestIntegrationWeChatNotifyHTTP(t *testing.T) {
	env := newEnv(t)
	orderID, enrollmentID, groupID := seedMockPayOrder(t, env, 10000)

	provider, signer := newRealWeChatProviderForTest(t)
	service := paymentsvc.New(paymentrepo.NewGORM(env.db), testLogger())
	handler := paymenthandler.New(provider, service)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/payments/notify", handler.Notify)

	body, headers := signer.buildNotify(t, orderID, 10000)

	// 1) 无签名头：401，不修改数据。
	rec := postWeChatNotify(engine, body, nil)
	assertNotifyError(t, rec, http.StatusUnauthorized, "PAY_SIGN_INVALID")

	// 2) 错误平台证书序列号：401。
	wrongSerial := headers.Clone()
	wrongSerial.Set("Wechatpay-Serial", "DEADBEEF")
	rec = postWeChatNotify(engine, body, wrongSerial)
	assertNotifyError(t, rec, http.StatusUnauthorized, "PAY_SIGN_INVALID")

	// 3) 篡改 body：401。
	rec = postWeChatNotify(engine, append(append([]byte(nil), body...), ' '), headers)
	assertNotifyError(t, rec, http.StatusUnauthorized, "PAY_SIGN_INVALID")

	// 三次失败后订单必须仍为 pending_payment。
	order := loadOrder(t, env, orderID)
	if order.Status != "pending_payment" || order.PaidAt != nil || order.TransactionID != nil {
		t.Fatalf("验签失败不应修改订单: %+v", order)
	}

	// 4) 合法签名：200，Order/Enrollment 迁移为 paid，Redis 不变。
	rec = postWeChatNotify(engine, body, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("合法回调 status = %d，期望 200: %s", rec.Code, rec.Body.String())
	}
	order = loadOrder(t, env, orderID)
	if order.Status != "paid" || order.PaidAt == nil || order.TransactionID == nil {
		t.Fatalf("合法回调后订单未迁移: %+v", order)
	}
	enrollment := loadEnrollment(t, env, enrollmentID)
	if enrollment.Status != "paid" || enrollment.PaidAt == nil {
		t.Fatalf("合法回调后报名未迁移: %+v", enrollment)
	}
	if remaining := redisInt(t, env, "group:"+groupID+":remaining"); remaining != 4 {
		t.Errorf("支付回调不应修改 Redis: remaining=%d，期望 4", remaining)
	}
	firstPaidAt := *order.PaidAt
	firstTransactionID := *order.TransactionID
	firstUpdatedAt := order.UpdatedAt

	// 5) 重复合法回调：200，幂等。
	rec = postWeChatNotify(engine, body, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("重复回调 status = %d，期望 200: %s", rec.Code, rec.Body.String())
	}
	order = loadOrder(t, env, orderID)
	if order.Status != "paid" {
		t.Errorf("重复回调后 Order.status = %q，期望 paid", order.Status)
	}
	if order.PaidAt == nil || !order.PaidAt.Equal(firstPaidAt) {
		t.Errorf("重复回调不应改变 Order.paid_at")
	}
	if order.TransactionID == nil || *order.TransactionID != firstTransactionID {
		t.Errorf("重复回调不应改变 transaction_id")
	}
	if !order.UpdatedAt.Equal(firstUpdatedAt) {
		t.Errorf("重复回调不应改变 Order.updated_at")
	}
}
