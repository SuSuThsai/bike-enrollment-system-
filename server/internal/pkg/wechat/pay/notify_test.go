package pay

import (
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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testAPIv3Key = "0123456789abcdef0123456789abcdef"

func TestNotifyDecoderVerifyAndDecrypt(t *testing.T) {
	decoder, key, serial := newNotifyTestDecoder(t)
	envelope, body := buildNotifyBody(t, key)
	headers := signNotifyHeaders(t, key, serial, body, "1700000000", "notify-nonce")

	if err := decoder.Verify(headers, body); err != nil {
		t.Fatalf("Verify 失败: %v", err)
	}
	transaction, err := decoder.Decrypt(envelope.Resource)
	if err != nil {
		t.Fatalf("Decrypt 失败: %v", err)
	}
	if transaction.OutTradeNo != "order-1" || transaction.TransactionID != "wx-transaction-1" ||
		transaction.Amount.Total != 10000 || transaction.TradeState != "SUCCESS" {
		t.Errorf("解密结果错误: %+v", transaction)
	}
}

func TestNotifyDecoderDecryptRefund(t *testing.T) {
	decoder, key, serial := newNotifyTestDecoder(t)

	refund := RefundTransaction{
		OutTradeNo:   "order-1",
		OutRefundNo:  "refund-1",
		RefundID:     "wx-refund-1",
		RefundStatus: "SUCCESS",
		Amount: RefundTransactionAmount{
			Total:       10000,
			Refund:      10000,
			PayerTotal:  10000,
			PayerRefund: 10000,
			Currency:    "CNY",
		},
	}
	plaintext, err := json.Marshal(refund)
	if err != nil {
		t.Fatalf("序列化退款交易失败: %v", err)
	}
	nonce := "123456789012"
	associatedData := "refund"
	envelope := NotifyEnvelope{
		ID:        "notify-refund-1",
		EventType: "REFUND.SUCCESS",
		Resource: NotifyResource{
			Algorithm:      "AEAD_AES_256_GCM",
			Ciphertext:     encryptNotifyResource(t, plaintext, nonce, associatedData),
			AssociatedData: associatedData,
			Nonce:          nonce,
		},
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("序列化退款回调失败: %v", err)
	}
	headers := signNotifyHeaders(t, key, serial, body, "1700000001", "refund-nonce")
	if err := decoder.Verify(headers, body); err != nil {
		t.Fatalf("退款回调 Verify 失败: %v", err)
	}

	result, err := decoder.DecryptRefund(envelope.Resource)
	if err != nil {
		t.Fatalf("DecryptRefund 失败: %v", err)
	}
	if result.OutTradeNo != "order-1" || result.OutRefundNo != "refund-1" ||
		result.RefundID != "wx-refund-1" || result.RefundStatus != "SUCCESS" ||
		result.Amount.Refund != 10000 {
		t.Errorf("退款解密结果错误: %+v", result)
	}
}

func TestNotifyDecoderRejectsTamperedBody(t *testing.T) {
	decoder, key, serial := newNotifyTestDecoder(t)
	_, body := buildNotifyBody(t, key)
	headers := signNotifyHeaders(t, key, serial, body, "1700000000", "notify-nonce")

	if err := decoder.Verify(headers, append(body, ' ')); err == nil {
		t.Fatal("篡改 body 后验签应失败")
	}
}

func TestNotifyDecoderRejectsWrongSerial(t *testing.T) {
	decoder, key, _ := newNotifyTestDecoder(t)
	_, body := buildNotifyBody(t, key)
	headers := signNotifyHeaders(t, key, "DEADBEEF", body, "1700000000", "notify-nonce")

	if err := decoder.Verify(headers, body); err == nil {
		t.Fatal("平台证书序列号不匹配时应失败")
	}
}

func newNotifyTestDecoder(t *testing.T) (*NotifyDecoder, *rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成平台私钥失败: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(0x1234abcd),
		Subject:      pkix.Name{CommonName: "wechatpay-test"},
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
	decoder, err := NewNotifyDecoder(NotifyConfig{
		PlatformCertPath: certPath,
		APIv3Key:         testAPIv3Key,
	})
	if err != nil {
		t.Fatalf("NewNotifyDecoder 失败: %v", err)
	}
	return decoder, key, strings.ToUpper(template.SerialNumber.Text(16))
}

func buildNotifyBody(t *testing.T, platformKey *rsa.PrivateKey) (NotifyEnvelope, []byte) {
	t.Helper()
	transaction := Transaction{
		OutTradeNo:    "order-1",
		TransactionID: "wx-transaction-1",
		TradeState:    "SUCCESS",
		Amount: TransactionAmount{
			Total:      10000,
			PayerTotal: 10000,
			Currency:   "CNY",
		},
	}
	plaintext, err := json.Marshal(transaction)
	if err != nil {
		t.Fatalf("序列化交易失败: %v", err)
	}
	nonce := "123456789012"
	associatedData := "transaction"
	ciphertext := encryptNotifyResource(t, plaintext, nonce, associatedData)

	envelope := NotifyEnvelope{
		ID:        "notify-1",
		EventType: "TRANSACTION.SUCCESS",
		Resource: NotifyResource{
			Algorithm:      "AEAD_AES_256_GCM",
			Ciphertext:     ciphertext,
			AssociatedData: associatedData,
			Nonce:          nonce,
		},
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("序列化回调信封失败: %v", err)
	}
	return envelope, body
}

func encryptNotifyResource(t *testing.T, plaintext []byte, nonce, associatedData string) string {
	t.Helper()
	block, err := aes.NewCipher([]byte(testAPIv3Key))
	if err != nil {
		t.Fatalf("创建 AES 失败: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("创建 GCM 失败: %v", err)
	}
	ciphertext := gcm.Seal(nil, []byte(nonce), plaintext, []byte(associatedData))
	return base64.StdEncoding.EncodeToString(ciphertext)
}

func signNotifyHeaders(t *testing.T, key *rsa.PrivateKey, serial string, body []byte, timestamp, nonce string) http.Header {
	t.Helper()
	message := timestamp + "\n" + nonce + "\n" + string(body) + "\n"
	digest := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("签名回调失败: %v", err)
	}
	headers := http.Header{}
	headers.Set(headerTimestamp, timestamp)
	headers.Set(headerNonce, nonce)
	headers.Set(headerSignature, base64.StdEncoding.EncodeToString(signature))
	headers.Set(headerSerial, serial)
	return headers
}
