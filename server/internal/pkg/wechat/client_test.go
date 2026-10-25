package wechat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCode2Session(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("appid") != "app-id" {
			t.Errorf("appid = %q", r.URL.Query().Get("appid"))
		}
		if r.URL.Query().Get("js_code") != "wx-code" {
			t.Errorf("js_code = %q", r.URL.Query().Get("js_code"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"openid":"openid-1","session_key":"session-1","unionid":"union-1"}`))
	}))
	defer server.Close()

	client := &Client{
		appID:      "app-id",
		appSecret:  "app-secret",
		endpoint:   server.URL,
		httpClient: server.Client(),
	}

	session, err := client.Code2Session(context.Background(), "wx-code")
	if err != nil {
		t.Fatalf("Code2Session 失败: %v", err)
	}
	if session.OpenID != "openid-1" || session.UnionID != "union-1" {
		t.Fatalf("Session = %+v", session)
	}
}

func TestCode2SessionInvalidCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":40029,"errmsg":"invalid code"}`))
	}))
	defer server.Close()

	client := &Client{
		appID:      "app-id",
		appSecret:  "app-secret",
		endpoint:   server.URL,
		httpClient: server.Client(),
	}

	_, err := client.Code2Session(context.Background(), "bad-code")
	if !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("err = %v，期望 ErrInvalidCode", err)
	}
}
