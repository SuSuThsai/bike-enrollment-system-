package pay

import (
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
)

const (
	headerTimestamp = "Wechatpay-Timestamp"
	headerNonce     = "Wechatpay-Nonce"
	headerSignature = "Wechatpay-Signature"
	headerSerial    = "Wechatpay-Serial"
)

// NotifyConfig 是微信支付 V3 回调验签/解密配置。
type NotifyConfig struct {
	PlatformCertPath string
	APIv3Key         string
}

// NotifyEnvelope 是微信支付 V3 回调信封。
type NotifyEnvelope struct {
	ID         string         `json:"id"`
	EventType  string         `json:"event_type"`
	Summary    string         `json:"summary"`
	CreateTime string         `json:"create_time"`
	Resource   NotifyResource `json:"resource"`
}

// NotifyResource 是回调资源密文。
type NotifyResource struct {
	Algorithm      string `json:"algorithm"`
	Ciphertext     string `json:"ciphertext"`
	AssociatedData string `json:"associated_data"`
	Nonce          string `json:"nonce"`
	OriginalType   string `json:"original_type"`
}

// Transaction 是 resource 解密后的支付结果。
type Transaction struct {
	OutTradeNo    string            `json:"out_trade_no"`
	TransactionID string            `json:"transaction_id"`
	TradeState    string            `json:"trade_state"`
	SuccessTime   string            `json:"success_time"`
	Amount        TransactionAmount `json:"amount"`
}

// TransactionAmount 是支付金额。
type TransactionAmount struct {
	Total      int    `json:"total"`
	PayerTotal int    `json:"payer_total"`
	Currency   string `json:"currency"`
}

// RefundTransaction 是退款回调 resource 解密后的明文。
type RefundTransaction struct {
	OutTradeNo   string                  `json:"out_trade_no"`
	OutRefundNo  string                  `json:"out_refund_no"`
	RefundID     string                  `json:"refund_id"`
	RefundStatus string                  `json:"refund_status"`
	SuccessTime  string                  `json:"success_time"`
	Amount       RefundTransactionAmount `json:"amount"`
}

// RefundTransactionAmount 是退款金额。
type RefundTransactionAmount struct {
	Total       int    `json:"total"`
	Refund      int    `json:"refund"`
	PayerTotal  int    `json:"payer_total"`
	PayerRefund int    `json:"payer_refund"`
	Currency    string `json:"currency"`
}

// NotifyDecoder 负责回调验签与 resource 解密。
type NotifyDecoder struct {
	publicKey *rsa.PublicKey
	serial    string
	apiV3Key  []byte
}

// NewNotifyDecoder 加载微信支付平台证书与 APIv3 密钥。
func NewNotifyDecoder(cfg NotifyConfig) (*NotifyDecoder, error) {
	if cfg.PlatformCertPath == "" || cfg.APIv3Key == "" {
		return nil, errors.New("wechat pay notify config incomplete")
	}
	if len(cfg.APIv3Key) != 32 {
		return nil, errors.New("wechat pay APIv3 key must be 32 bytes")
	}

	data, err := os.ReadFile(cfg.PlatformCertPath)
	if err != nil {
		return nil, fmt.Errorf("read platform certificate: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("platform certificate is not PEM encoded")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse platform certificate: %w", err)
	}
	publicKey, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("platform certificate public key is not RSA")
	}

	return &NotifyDecoder{
		publicKey: publicKey,
		serial:    strings.ToUpper(cert.SerialNumber.Text(16)),
		apiV3Key:  []byte(cfg.APIv3Key),
	}, nil
}

// Verify 校验微信支付 V3 回调签名。
func (d *NotifyDecoder) Verify(headers http.Header, body []byte) error {
	timestamp := headers.Get(headerTimestamp)
	nonce := headers.Get(headerNonce)
	signature := headers.Get(headerSignature)
	serial := headers.Get(headerSerial)
	if timestamp == "" || nonce == "" || signature == "" || serial == "" {
		return errors.New("missing wechat pay signature headers")
	}
	if !strings.EqualFold(serial, d.serial) {
		return errors.New("wechat pay platform serial mismatch")
	}

	signatureBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("decode wechat pay signature: %w", err)
	}
	message := timestamp + "\n" + nonce + "\n" + string(body) + "\n"
	digest := sha256.Sum256([]byte(message))
	if err := rsa.VerifyPKCS1v15(d.publicKey, crypto.SHA256, digest[:], signatureBytes); err != nil {
		return fmt.Errorf("verify wechat pay signature: %w", err)
	}
	return nil
}

// Decrypt 使用 APIv3 密钥解密支付回调 resource。
func (d *NotifyDecoder) Decrypt(resource NotifyResource) (*Transaction, error) {
	plaintext, err := d.decryptResource(resource)
	if err != nil {
		return nil, err
	}

	var transaction Transaction
	if err := json.Unmarshal(plaintext, &transaction); err != nil {
		return nil, fmt.Errorf("decode transaction: %w", err)
	}
	return &transaction, nil
}

// DecryptRefund 使用 APIv3 密钥解密退款回调 resource。
func (d *NotifyDecoder) DecryptRefund(resource NotifyResource) (*RefundTransaction, error) {
	plaintext, err := d.decryptResource(resource)
	if err != nil {
		return nil, err
	}

	var transaction RefundTransaction
	if err := json.Unmarshal(plaintext, &transaction); err != nil {
		return nil, fmt.Errorf("decode refund transaction: %w", err)
	}
	return &transaction, nil
}

func (d *NotifyDecoder) decryptResource(resource NotifyResource) ([]byte, error) {
	if resource.Algorithm != "AEAD_AES_256_GCM" {
		return nil, fmt.Errorf("unsupported wechat pay algorithm %q", resource.Algorithm)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(resource.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decode resource ciphertext: %w", err)
	}
	if len(resource.Nonce) != 12 {
		return nil, errors.New("wechat pay resource nonce must be 12 bytes")
	}

	block, err := aes.NewCipher(d.apiV3Key)
	if err != nil {
		return nil, fmt.Errorf("create aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}
	plaintext, err := gcm.Open(nil, []byte(resource.Nonce), ciphertext, []byte(resource.AssociatedData))
	if err != nil {
		return nil, fmt.Errorf("decrypt resource: %w", err)
	}
	return plaintext, nil
}
