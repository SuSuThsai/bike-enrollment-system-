// Package pay 实现微信支付 V3 JSAPI 下单与小程序支付参数签名。
package pay

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	jsapiPath      = "/v3/pay/transactions/jsapi"
	refundPath     = "/v3/refund/domestic/refunds"
	defaultBaseURL = "https://api.mch.weixin.qq.com"
)

// Config 是微信支付 V3 商户配置。
type Config struct {
	AppID          string
	MchID          string
	SerialNo       string
	NotifyURL      string
	PrivateKeyPath string
	BaseURL        string
	HTTPClient     *http.Client
}

// PrepayRequest 是微信支付 JSAPI 统一下单请求体。
type PrepayRequest struct {
	AppID       string `json:"appid"`
	MchID       string `json:"mchid"`
	Description string `json:"description"`
	OutTradeNo  string `json:"out_trade_no"`
	NotifyURL   string `json:"notify_url"`
	Amount      Amount `json:"amount"`
	Payer       Payer  `json:"payer"`
}

// Amount 是订单金额，单位分。
type Amount struct {
	Total    int    `json:"total"`
	Currency string `json:"currency"`
}

// Payer 是支付用户标识。
type Payer struct {
	OpenID string `json:"openid"`
}

// 微信支付退款申请状态。
const (
	RefundStatusSuccess    = "SUCCESS"
	RefundStatusProcessing = "PROCESSING"
)

// RefundRequest 是微信支付 V3 退款申请请求体。
type RefundRequest struct {
	OutTradeNo  string       `json:"out_trade_no"`
	OutRefundNo string       `json:"out_refund_no"`
	Amount      RefundAmount `json:"amount"`
}

// RefundAmount 是退款金额信息，单位分。
type RefundAmount struct {
	Refund   int    `json:"refund"`
	Total    int    `json:"total"`
	Currency string `json:"currency"`
}

// RefundResponse 是微信支付 V3 退款申请响应。
type RefundResponse struct {
	RefundID    string `json:"refund_id"`
	OutRefundNo string `json:"out_refund_no"`
	Status      string `json:"status"`
}

// PayParams 是小程序 wx.requestPayment 所需参数。
type PayParams struct {
	TimeStamp string `json:"timeStamp"`
	NonceStr  string `json:"nonceStr"`
	Package   string `json:"package"`
	SignType  string `json:"signType"`
	PaySign   string `json:"paySign"`
}

// Client 是微信支付 V3 客户端。
type Client struct {
	appID      string
	mchID      string
	serialNo   string
	notifyURL  string
	baseURL    string
	privateKey *rsa.PrivateKey
	httpClient *http.Client
}

// NewClient 加载商户私钥并创建客户端。
func NewClient(cfg Config) (*Client, error) {
	if cfg.AppID == "" || cfg.MchID == "" || cfg.SerialNo == "" || cfg.NotifyURL == "" || cfg.PrivateKeyPath == "" {
		return nil, errors.New("wechat pay config incomplete")
	}
	key, err := loadPrivateKey(cfg.PrivateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("load wechat pay private key: %w", err)
	}

	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	return &Client{
		appID:      cfg.AppID,
		mchID:      cfg.MchID,
		serialNo:   cfg.SerialNo,
		notifyURL:  cfg.NotifyURL,
		baseURL:    baseURL,
		privateKey: key,
		httpClient: httpClient,
	}, nil
}

// Prepay 调用微信支付 V3 JSAPI 统一下单接口，返回 prepay_id。
func (c *Client) Prepay(ctx context.Context, req PrepayRequest) (string, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal prepay request: %w", err)
	}

	authorization, err := c.authorization(http.MethodPost, jsapiPath, body, time.Now())
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+jsapiPath, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build prepay request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "bike-enroll-api/1.0")
	httpReq.Header.Set("Authorization", authorization)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("call wechat pay prepay: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read wechat pay prepay response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("wechat pay prepay http %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var result struct {
		PrepayID string `json:"prepay_id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("decode wechat pay prepay response: %w", err)
	}
	if result.PrepayID == "" {
		return "", errors.New("wechat pay prepay response missing prepay_id")
	}
	return result.PrepayID, nil
}

// Refund 调用微信支付 V3 退款接口。
func (c *Client) Refund(ctx context.Context, req RefundRequest) (*RefundResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal refund request: %w", err)
	}

	authorization, err := c.authorization(http.MethodPost, refundPath, body, time.Now())
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+refundPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build refund request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "bike-enroll-api/1.0")
	httpReq.Header.Set("Authorization", authorization)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call wechat pay refund: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read wechat pay refund response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("wechat pay refund http %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var result RefundResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decode wechat pay refund response: %w", err)
	}
	if result.RefundID == "" {
		return nil, errors.New("wechat pay refund response missing refund_id")
	}
	return &result, nil
}

// BuildPayParams 生成小程序 wx.requestPayment 所需的 RSA 签名参数。
func (c *Client) BuildPayParams(prepayID string, now time.Time) (PayParams, error) {
	if prepayID == "" {
		return PayParams{}, errors.New("prepay_id is empty")
	}
	nonce, err := randomNonce()
	if err != nil {
		return PayParams{}, err
	}
	timeStamp := strconv.FormatInt(now.Unix(), 10)
	packageValue := "prepay_id=" + prepayID
	message := c.appID + "\n" + timeStamp + "\n" + nonce + "\n" + packageValue + "\n"
	signature, err := sign(c.privateKey, message)
	if err != nil {
		return PayParams{}, err
	}

	return PayParams{
		TimeStamp: timeStamp,
		NonceStr:  nonce,
		Package:   packageValue,
		SignType:  "RSA",
		PaySign:   signature,
	}, nil
}

func (c *Client) authorization(method, path string, body []byte, now time.Time) (string, error) {
	nonce, err := randomNonce()
	if err != nil {
		return "", err
	}
	timeStamp := strconv.FormatInt(now.Unix(), 10)
	message := method + "\n" + path + "\n" + timeStamp + "\n" + nonce + "\n" + string(body) + "\n"
	signature, err := sign(c.privateKey, message)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		`WECHATPAY2-SHA256-RSA2048 mchid="%s",nonce_str="%s",signature="%s",timestamp="%s",serial_no="%s"`,
		c.mchID,
		nonce,
		signature,
		timeStamp,
		c.serialNo,
	), nil
}

func sign(key *rsa.PrivateKey, message string) (string, error) {
	digest := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign wechat pay message: %w", err)
	}
	return base64.StdEncoding.EncodeToString(signature), nil
}

func randomNonce() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func loadPrivateKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read private key file: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("private key is not PEM encoded")
	}

	if parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		key, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("private key is not RSA")
		}
		return key, nil
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, errors.New("parse private key failed")
}
