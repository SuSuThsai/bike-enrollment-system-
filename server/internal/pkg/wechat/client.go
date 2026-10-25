// Package wechat 封装微信小程序服务端接口。
package wechat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const code2SessionEndpoint = "https://api.weixin.qq.com/sns/jscode2session"

// ErrInvalidCode 表示微信返回 code 无效或已被使用。
var ErrInvalidCode = errors.New("wechat code invalid")

// Session 是 code2session 的有效返回。
type Session struct {
	OpenID     string
	SessionKey string
	UnionID    string
}

// Client 调用微信小程序服务端接口。
type Client struct {
	appID      string
	appSecret  string
	endpoint   string
	httpClient *http.Client
}

// NewClient 创建微信客户端。
func NewClient(appID, secret string) *Client {
	return &Client{
		appID:     strings.TrimSpace(appID),
		appSecret: strings.TrimSpace(secret),
		endpoint:  code2SessionEndpoint,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

type code2SessionResponse struct {
	OpenID     string `json:"openid"`
	SessionKey string `json:"session_key"`
	UnionID    string `json:"unionid"`
	ErrCode    int    `json:"errcode"`
	ErrMsg     string `json:"errmsg"`
}

// Code2Session 使用 wx.login 的 code 换取 openid。
func (c *Client) Code2Session(ctx context.Context, code string) (Session, error) {
	if c.appID == "" || c.appSecret == "" {
		return Session{}, errors.New("wechat appid/secret is not configured")
	}

	query := url.Values{}
	query.Set("appid", c.appID)
	query.Set("secret", c.appSecret)
	query.Set("js_code", code)
	query.Set("grant_type", "authorization_code")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return Session{}, fmt.Errorf("build code2session request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Session{}, fmt.Errorf("call code2session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Session{}, fmt.Errorf("code2session http status %d", resp.StatusCode)
	}

	var payload code2SessionResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return Session{}, fmt.Errorf("decode code2session response: %w", err)
	}

	if payload.ErrCode != 0 {
		// 40029: code 无效；40163: code 已被使用。
		if payload.ErrCode == 40029 || payload.ErrCode == 40163 {
			return Session{}, fmt.Errorf("%w: errcode=%d", ErrInvalidCode, payload.ErrCode)
		}
		return Session{}, fmt.Errorf("code2session errcode=%d errmsg=%s", payload.ErrCode, payload.ErrMsg)
	}
	if payload.OpenID == "" {
		return Session{}, errors.New("code2session returned empty openid")
	}

	return Session{
		OpenID:     payload.OpenID,
		SessionKey: payload.SessionKey,
		UnionID:    payload.UnionID,
	}, nil
}
