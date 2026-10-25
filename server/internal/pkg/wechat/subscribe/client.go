// Package subscribe 实现微信小程序订阅消息发送。
package subscribe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const defaultBaseURL = "https://api.weixin.qq.com"

var errAccessTokenInvalid = errors.New("wechat access token invalid")

// Config 是微信订阅消息客户端配置。
type Config struct {
	AppID      string
	Secret     string
	BaseURL    string
	HTTPClient *http.Client
}

// Client 是微信订阅消息客户端，带 access_token 缓存。
type Client struct {
	appID      string
	secret     string
	baseURL    string
	httpClient *http.Client

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

// NewClient 创建订阅消息客户端。
func NewClient(cfg Config) (*Client, error) {
	if cfg.AppID == "" || cfg.Secret == "" {
		return nil, errors.New("wechat subscribe config incomplete")
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
		secret:     cfg.Secret,
		baseURL:    baseURL,
		httpClient: httpClient,
	}, nil
}

// SendSubscribeMessage 调用微信订阅消息发送接口。
func (c *Client) SendSubscribeMessage(ctx context.Context, openID, templateID string, data map[string]any) error {
	token, err := c.accessToken(ctx, false)
	if err != nil {
		return err
	}
	err = c.sendWithToken(ctx, token, openID, templateID, data)
	if errors.Is(err, errAccessTokenInvalid) {
		token, refreshErr := c.accessToken(ctx, true)
		if refreshErr != nil {
			return refreshErr
		}
		return c.sendWithToken(ctx, token, openID, templateID, data)
	}
	return err
}

func (c *Client) sendWithToken(ctx context.Context, token, openID, templateID string, data map[string]any) error {
	payload := map[string]any{
		"touser":      openID,
		"template_id": templateID,
		"data":        data,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal subscribe message: %w", err)
	}

	endpoint := c.baseURL + "/cgi-bin/message/subscribe/send?access_token=" + url.QueryEscape(token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build subscribe message request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call subscribe message api: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read subscribe message response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("subscribe message http %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var result struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("decode subscribe message response: %w", err)
	}
	if result.ErrCode == 40001 || result.ErrCode == 42001 || result.ErrCode == 40014 {
		return errAccessTokenInvalid
	}
	if result.ErrCode != 0 {
		return fmt.Errorf("wechat subscribe send failed: errcode=%d errmsg=%s", result.ErrCode, result.ErrMsg)
	}
	return nil
}

func (c *Client) accessToken(ctx context.Context, forceRefresh bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !forceRefresh && c.token != "" && time.Now().Before(c.expiresAt) {
		return c.token, nil
	}

	endpoint := fmt.Sprintf(
		"%s/cgi-bin/token?grant_type=client_credential&appid=%s&secret=%s",
		c.baseURL,
		url.QueryEscape(c.appID),
		url.QueryEscape(c.secret),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("build access token request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("call access token api: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read access token response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("access token http %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("decode access token response: %w", err)
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("wechat access token failed: errcode=%d errmsg=%s", result.ErrCode, result.ErrMsg)
	}
	if result.ExpiresIn <= 0 {
		result.ExpiresIn = 7200
	}
	ttl := result.ExpiresIn - 60
	if ttl <= 0 {
		ttl = 1
	}
	c.token = result.AccessToken
	c.expiresAt = time.Now().Add(time.Duration(ttl) * time.Second)
	return c.token, nil
}
