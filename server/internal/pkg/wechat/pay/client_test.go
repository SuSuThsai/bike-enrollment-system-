package pay

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrepaySignsRequestAndParsesPrepayID(t *testing.T) {
	pub, keyPath := generateTestKey(t)

	var gotPath string
	var gotAuth string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"prepay_id":"prepay-123"}`)
	}))
	defer server.Close()

	client, err := NewClient(Config{
		AppID:          "wx-appid",
		MchID:          "mch-id",
		SerialNo:       "serial-no",
		NotifyURL:      "https://example.com/notify",
		PrivateKeyPath: keyPath,
		BaseURL:        server.URL,
	})
	if err != nil {
		t.Fatalf("NewClient 失败: %v", err)
	}

	req := PrepayRequest{
		AppID:       "wx-appid",
		MchID:       "mch-id",
		Description: "赛事报名-order-1",
		OutTradeNo:  "order-1",
		NotifyURL:   "https://example.com/notify",
		Amount:      Amount{Total: 10000, Currency: "CNY"},
		Payer:       Payer{OpenID: "openid-1"},
	}
	prepayID, err := client.Prepay(context.Background(), req)
	if err != nil {
		t.Fatalf("Prepay 失败: %v", err)
	}
	if prepayID != "prepay-123" {
		t.Errorf("prepay_id = %q，期望 prepay-123", prepayID)
	}
	if gotPath != jsapiPath {
		t.Errorf("path = %q，期望 %q", gotPath, jsapiPath)
	}

	var sent PrepayRequest
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("请求体不是合法 JSON: %v", err)
	}
	if sent.OutTradeNo != "order-1" || sent.Amount.Total != 10000 || sent.Payer.OpenID != "openid-1" {
		t.Errorf("请求体组装错误: %+v", sent)
	}

	attrs := parseAuthorization(t, gotAuth)
	if attrs["mchid"] != "mch-id" || attrs["serial_no"] != "serial-no" {
		t.Errorf("Authorization 商户信息错误: %+v", attrs)
	}
	message := "POST\n" + jsapiPath + "\n" + attrs["timestamp"] + "\n" + attrs["nonce_str"] + "\n" + string(gotBody) + "\n"
	verifySignature(t, pub, message, attrs["signature"])
}

func TestRefundSignsRequestAndParsesStatus(t *testing.T) {
	pub, keyPath := generateTestKey(t)

	var gotPath string
	var gotAuth string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"refund_id":"refund-123","out_refund_no":"refund-1","status":"PROCESSING"}`)
	}))
	defer server.Close()

	client, err := NewClient(Config{
		AppID:          "wx-appid",
		MchID:          "mch-id",
		SerialNo:       "serial-no",
		NotifyURL:      "https://example.com/notify",
		PrivateKeyPath: keyPath,
		BaseURL:        server.URL,
	})
	if err != nil {
		t.Fatalf("NewClient 失败: %v", err)
	}

	response, err := client.Refund(context.Background(), RefundRequest{
		OutTradeNo:  "order-1",
		OutRefundNo: "refund-1",
		Amount:      RefundAmount{Refund: 100, Total: 100, Currency: "CNY"},
	})
	if err != nil {
		t.Fatalf("Refund 失败: %v", err)
	}
	if response.RefundID != "refund-123" || response.Status != RefundStatusProcessing {
		t.Errorf("退款响应错误: %+v", response)
	}
	if gotPath != refundPath {
		t.Errorf("path = %q，期望 %q", gotPath, refundPath)
	}

	var sent RefundRequest
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("请求体不是合法 JSON: %v", err)
	}
	if sent.OutTradeNo != "order-1" || sent.OutRefundNo != "refund-1" ||
		sent.Amount.Refund != 100 || sent.Amount.Total != 100 || sent.Amount.Currency != "CNY" {
		t.Errorf("退款请求体组装错误: %+v", sent)
	}

	attrs := parseAuthorization(t, gotAuth)
	message := "POST\n" + refundPath + "\n" + attrs["timestamp"] + "\n" + attrs["nonce_str"] + "\n" + string(gotBody) + "\n"
	verifySignature(t, pub, message, attrs["signature"])
}

func TestBuildPayParams(t *testing.T) {
	pub, keyPath := generateTestKey(t)
	client, err := NewClient(Config{
		AppID:          "wx-appid",
		MchID:          "mch-id",
		SerialNo:       "serial-no",
		NotifyURL:      "https://example.com/notify",
		PrivateKeyPath: keyPath,
	})
	if err != nil {
		t.Fatalf("NewClient 失败: %v", err)
	}

	now := time.Unix(1700000000, 0)
	params, err := client.BuildPayParams("prepay-123", now)
	if err != nil {
		t.Fatalf("BuildPayParams 失败: %v", err)
	}
	if params.TimeStamp != "1700000000" {
		t.Errorf("timeStamp = %q，期望 1700000000", params.TimeStamp)
	}
	if params.Package != "prepay_id=prepay-123" {
		t.Errorf("package = %q", params.Package)
	}
	if params.SignType != "RSA" {
		t.Errorf("signType = %q，期望 RSA", params.SignType)
	}
	if params.NonceStr == "" || len(params.NonceStr) > 32 {
		t.Errorf("nonceStr 长度非法: %q", params.NonceStr)
	}

	message := "wx-appid\n" + params.TimeStamp + "\n" + params.NonceStr + "\n" + params.Package + "\n"
	verifySignature(t, pub, message, params.PaySign)
}

func generateTestKey(t *testing.T) (*rsa.PublicKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成测试私钥失败: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("编码测试私钥失败: %v", err)
	}
	pemData := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	path := filepath.Join(t.TempDir(), "apiclient_key.pem")
	if err := os.WriteFile(path, pemData, 0o600); err != nil {
		t.Fatalf("写入测试私钥失败: %v", err)
	}
	return &key.PublicKey, path
}

func parseAuthorization(t *testing.T, header string) map[string]string {
	t.Helper()
	const prefix = "WECHATPAY2-SHA256-RSA2048 "
	if !strings.HasPrefix(header, prefix) {
		t.Fatalf("Authorization 前缀错误: %q", header)
	}
	attrs := map[string]string{}
	for _, part := range strings.Split(strings.TrimPrefix(header, prefix), ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			t.Fatalf("Authorization 字段格式错误: %q", part)
		}
		attrs[kv[0]] = strings.Trim(kv[1], `"`)
	}
	return attrs
}

func verifySignature(t *testing.T, pub *rsa.PublicKey, message, signature string) {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		t.Fatalf("签名不是合法 base64: %v", err)
	}
	digest := sha256.Sum256([]byte(message))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], raw); err != nil {
		t.Fatalf("签名验证失败: %v", err)
	}
}
