package subscribe

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendSubscribeMessageCachesAccessToken(t *testing.T) {
	tokenCalls := 0
	sendCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/token":
			tokenCalls++
			_, _ = io.WriteString(w, `{"access_token":"token-1","expires_in":7200}`)
		case "/cgi-bin/message/subscribe/send":
			sendCalls++
			if r.URL.Query().Get("access_token") != "token-1" {
				t.Errorf("access_token = %q", r.URL.Query().Get("access_token"))
			}
			var payload struct {
				ToUser     string         `json:"touser"`
				TemplateID string         `json:"template_id"`
				Data       map[string]any `json:"data"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("请求体不是合法 JSON: %v", err)
			}
			if payload.ToUser != "openid-1" || payload.TemplateID != "tpl-1" || payload.Data["thing1"] == nil {
				t.Errorf("请求体错误: %+v", payload)
			}
			_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{
		AppID:   "appid",
		Secret:  "secret",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("NewClient 失败: %v", err)
	}
	data := map[string]any{"thing1": map[string]string{"value": "event"}}
	if err := client.SendSubscribeMessage(context.Background(), "openid-1", "tpl-1", data); err != nil {
		t.Fatalf("第一次发送失败: %v", err)
	}
	if err := client.SendSubscribeMessage(context.Background(), "openid-1", "tpl-1", data); err != nil {
		t.Fatalf("第二次发送失败: %v", err)
	}
	if tokenCalls != 1 || sendCalls != 2 {
		t.Errorf("tokenCalls=%d sendCalls=%d，期望 1/2（access_token 应缓存）", tokenCalls, sendCalls)
	}
}
