package auth_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"bike-enroll/server/internal/handler/auth"
	"bike-enroll/server/internal/model"
	"bike-enroll/server/internal/pkg/jwt"
	"bike-enroll/server/internal/pkg/wechat"
	userrepo "bike-enroll/server/internal/repository/user"
	"bike-enroll/server/internal/requestid"
	"bike-enroll/server/internal/response"
	authsvc "bike-enroll/server/internal/service/auth"
)

const testJWTSecret = "0123456789abcdef0123456789abcdef"

type fakeWeChat struct {
	session wechat.Session
	err     error
	calls   int
}

func (f *fakeWeChat) Code2Session(context.Context, string) (wechat.Session, error) {
	f.calls++
	if f.err != nil {
		return wechat.Session{}, f.err
	}
	return f.session, nil
}

type fakeUserRepo struct {
	users       map[string]*model.User
	createCalls int
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{users: make(map[string]*model.User)}
}

func (f *fakeUserRepo) FindByOpenID(_ context.Context, openid string) (*model.User, error) {
	user, ok := f.users[openid]
	if !ok {
		return nil, userrepo.ErrNotFound
	}
	return user, nil
}

func (f *fakeUserRepo) Create(_ context.Context, user *model.User) error {
	f.createCalls++
	if _, exists := f.users[user.OpenID]; exists {
		return userrepo.ErrDuplicate
	}
	f.users[user.OpenID] = user
	return nil
}

type loginResponse struct {
	Token string `json:"token"`
	User  struct {
		ID        string `json:"id"`
		Nickname  string `json:"nickname"`
		AvatarURL string `json:"avatar_url"`
		Role      string `json:"role"`
	} `json:"user"`
}

func newTestEngine(t *testing.T, wx *fakeWeChat, repo *fakeUserRepo) (http.Handler, *jwt.Manager) {
	t.Helper()

	gin.SetMode(gin.TestMode)
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))

	manager, err := jwt.NewManager(testJWTSecret, time.Hour)
	if err != nil {
		t.Fatalf("创建 JWT manager 失败: %v", err)
	}

	handler := auth.New(authsvc.New(repo, wx, manager))
	engine := gin.New()
	engine.Use(requestid.Middleware())
	engine.POST("/api/v1/auth/wechat-login", handler.WechatLogin)
	return engine, manager
}

func postLogin(engine http.Handler, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/wechat-login", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func TestWechatLogin(t *testing.T) {
	t.Run("新用户创建并签发 JWT", func(t *testing.T) {
		wx := &fakeWeChat{session: wechat.Session{OpenID: "openid-new", UnionID: "unionid-new"}}
		repo := newFakeUserRepo()
		engine, manager := newTestEngine(t, wx, repo)

		recorder := postLogin(engine, `{"code":"wx-code"}`)
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，期望 200；响应体：%s", recorder.Code, recorder.Body.String())
		}

		var body loginResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("解析响应失败: %v", err)
		}
		if body.Token == "" {
			t.Fatal("token 为空")
		}
		if body.User.ID == "" || body.User.Role != "rider" {
			t.Fatalf("用户响应不符合契约: %+v", body.User)
		}
		if body.User.Nickname != "微信用户" {
			t.Errorf("新用户 nickname = %q，期望 微信用户", body.User.Nickname)
		}
		if body.User.AvatarURL != "" {
			t.Errorf("新用户 avatar_url = %q，期望空字符串", body.User.AvatarURL)
		}

		if repo.createCalls != 1 || len(repo.users) != 1 {
			t.Fatalf("新用户应创建一次，实际 createCalls=%d users=%d", repo.createCalls, len(repo.users))
		}
		created, ok := repo.users["openid-new"]
		if !ok {
			t.Fatal("未按 openid 创建用户")
		}
		if created.UnionID != "unionid-new" {
			t.Errorf("unionid = %q，期望 unionid-new", created.UnionID)
		}

		claims, err := manager.Parse(body.Token)
		if err != nil {
			t.Fatalf("解析 JWT 失败: %v", err)
		}
		if claims.Subject != body.User.ID {
			t.Errorf("JWT sub = %q，期望 %q", claims.Subject, body.User.ID)
		}
		if claims.Role != "rider" {
			t.Errorf("JWT role = %q，期望 rider", claims.Role)
		}
		if claims.ExpiresAt == nil || claims.IssuedAt == nil {
			t.Fatal("JWT 缺少 iat/exp")
		}
		if got := claims.ExpiresAt.Time.Sub(claims.IssuedAt.Time); got != time.Hour {
			t.Errorf("JWT 有效期 = %v，期望 1h", got)
		}
	})

	t.Run("老用户复用且不重复创建", func(t *testing.T) {
		existing := &model.User{
			ID:       "user-existing",
			OpenID:   "openid-existing",
			Nickname: "老用户",
			Role:     "rider",
		}
		repo := newFakeUserRepo()
		repo.users[existing.OpenID] = existing
		wx := &fakeWeChat{session: wechat.Session{OpenID: existing.OpenID}}
		engine, manager := newTestEngine(t, wx, repo)

		recorder := postLogin(engine, `{"code":"wx-code"}`)
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，期望 200；响应体：%s", recorder.Code, recorder.Body.String())
		}

		var body loginResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("解析响应失败: %v", err)
		}
		if body.User.ID != existing.ID {
			t.Errorf("用户 ID = %q，期望复用 %q", body.User.ID, existing.ID)
		}
		if repo.createCalls != 0 {
			t.Errorf("老用户不应创建，实际 createCalls=%d", repo.createCalls)
		}

		claims, err := manager.Parse(body.Token)
		if err != nil {
			t.Fatalf("解析 JWT 失败: %v", err)
		}
		if claims.Subject != existing.ID {
			t.Errorf("JWT sub = %q，期望 %q", claims.Subject, existing.ID)
		}
	})

	t.Run("code 无效返回 400 AUTH_INVALID_CODE", func(t *testing.T) {
		wx := &fakeWeChat{err: fmt.Errorf("%w: errcode=40029", wechat.ErrInvalidCode)}
		repo := newFakeUserRepo()
		engine, _ := newTestEngine(t, wx, repo)

		recorder := postLogin(engine, `{"code":"invalid-code"}`)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("状态码 = %d，期望 400；响应体：%s", recorder.Code, recorder.Body.String())
		}

		var body response.ErrorResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("解析错误响应失败: %v", err)
		}
		if body.Code != response.CodeAuthInvalidCode {
			t.Errorf("code = %q，期望 %q", body.Code, response.CodeAuthInvalidCode)
		}
		if body.RequestID == "" {
			t.Error("错误响应缺少 request_id")
		}
		if recorder.Header().Get(requestid.Header) != body.RequestID {
			t.Error("响应头 X-Request-ID 与响应体 request_id 不一致")
		}
		if repo.createCalls != 0 {
			t.Errorf("code 无效时不应创建用户，实际 createCalls=%d", repo.createCalls)
		}
	})

	t.Run("缺少 code 返回 400 INVALID_ARGUMENT", func(t *testing.T) {
		repo := newFakeUserRepo()
		wx := &fakeWeChat{}
		engine, _ := newTestEngine(t, wx, repo)

		recorder := postLogin(engine, `{}`)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("状态码 = %d，期望 400；响应体：%s", recorder.Code, recorder.Body.String())
		}

		var body response.ErrorResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("解析错误响应失败: %v", err)
		}
		if body.Code != response.CodeInvalidArgument {
			t.Errorf("code = %q，期望 %q", body.Code, response.CodeInvalidArgument)
		}
		if wx.calls != 0 {
			t.Errorf("缺少 code 时不应调用微信接口，实际 calls=%d", wx.calls)
		}
	})
}
