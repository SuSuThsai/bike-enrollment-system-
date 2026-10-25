package contract

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	jwtlib "github.com/golang-jwt/jwt/v5"

	adminhandler "bike-enroll/server/internal/handler/admin"
	authhandler "bike-enroll/server/internal/handler/auth"
	enrollmenthandler "bike-enroll/server/internal/handler/enrollment"
	eventhandler "bike-enroll/server/internal/handler/event"
	notificationhandler "bike-enroll/server/internal/handler/notification"
	paymenthandler "bike-enroll/server/internal/handler/payment"
	"bike-enroll/server/internal/model"
	jwtx "bike-enroll/server/internal/pkg/jwt"
	paymentpkg "bike-enroll/server/internal/pkg/payment"
	adminrepo "bike-enroll/server/internal/repository/admin"
	"bike-enroll/server/internal/response"
	"bike-enroll/server/internal/router"
	adminsvc "bike-enroll/server/internal/service/admin"
	authsvc "bike-enroll/server/internal/service/auth"
	enrollmentsvc "bike-enroll/server/internal/service/enrollment"
	eventsvc "bike-enroll/server/internal/service/event"
)

const apiPrefix = "/api/v1"

// apiRequest 是契约测试要发出的请求描述。
type apiRequest struct {
	Method     string
	Path       string
	PathValues map[string]string
	Query      url.Values
	Body       []byte
	Token      string
}

// errorCase 描述一个可由客户端触发、且已在 OpenAPI 中声明状态码的错误场景。
type errorCase struct {
	Status int
	Apply  func(*apiRequest)
}

// pathContract 是单个 method/path 的契约测试配置。
type pathContract struct {
	Method string
	Path   string
	Error  *errorCase

	// SkipValidReason 用于无法构造合法请求的操作（例如依赖微信签名的回调）。
	SkipValidReason string

	// SkipMissingReason 用于“先验签、后解析”的接口：缺失字段但无合法签名时只能返回 401。
	SkipMissingReason string
}

// contractPaths 与 docs/openapi.yaml 的 paths 一一对应。
//
// 错误场景只挑选 OpenAPI 已声明的状态码：
//   - 受保护接口：不带 Bearer token 返回 401 AUTH_INVALID_TOKEN；
//   - 登录：非法 code 返回 400 AUTH_INVALID_CODE；
//   - 赛事详情：不存在的 event_id 返回 404 EVENT_NOT_FOUND；
//   - 支付回调：无签名的请求返回 401 PAY_SIGN_INVALID。
var contractPaths = []pathContract{
	{
		Method: http.MethodPost,
		Path:   "/auth/wechat-login",
		Error: &errorCase{Status: http.StatusBadRequest, Apply: func(r *apiRequest) {
			r.Body = []byte(`{"code":"invalid-code"}`)
		}},
	},
	{
		Method: http.MethodGet,
		Path:   "/events",
		// OpenAPI 只定义了 500，未定义可由客户端触发的 4xx。
	},
	{
		Method: http.MethodGet,
		Path:   "/events/{event_id}",
		Error:  &errorCase{Status: http.StatusNotFound, Apply: eventNotFound},
	},
	{
		Method: http.MethodPost,
		Path:   "/enrollments/lock",
		Error:  &errorCase{Status: http.StatusUnauthorized, Apply: withoutAuth},
	},
	{
		Method: http.MethodPost,
		Path:   "/enrollments",
		Error:  &errorCase{Status: http.StatusUnauthorized, Apply: withoutAuth},
	},
	{
		Method: http.MethodGet,
		Path:   "/enrollments/my",
		Error:  &errorCase{Status: http.StatusUnauthorized, Apply: withoutAuth},
	},
	{
		Method: http.MethodGet,
		Path:   "/enrollments/{enrollment_id}",
		Error:  &errorCase{Status: http.StatusUnauthorized, Apply: withoutAuth},
	},
	{
		Method: http.MethodPost,
		Path:   "/payments/prepay",
		Error:  &errorCase{Status: http.StatusUnauthorized, Apply: withoutAuth},
	},
	{
		Method: http.MethodPost,
		Path:   "/payments/notify",
		Error:  &errorCase{Status: http.StatusUnauthorized},
		SkipValidReason: "微信支付回调的成功请求依赖微信 V3 签名头，OpenAPI 未定义该输入；" +
			"需先接入签名夹具（或联调环境）再启用合法请求断言",
		SkipMissingReason: "微信支付回调必须先验签；缺失字段请求没有合法签名，按契约返回 401，无法验证 400 分支",
	},
	{
		Method: http.MethodPost,
		Path:   "/notifications/subscribe",
		Error:  &errorCase{Status: http.StatusUnauthorized, Apply: withoutAuth},
	},
	{
		Method: http.MethodPost,
		Path:   "/admin/events",
		Error:  &errorCase{Status: http.StatusUnauthorized, Apply: withoutAuth},
	},
	{
		Method: http.MethodPut,
		Path:   "/admin/events/{event_id}",
		Error:  &errorCase{Status: http.StatusUnauthorized, Apply: withoutAuth},
	},
	{
		Method: http.MethodPost,
		Path:   "/admin/groups",
		Error:  &errorCase{Status: http.StatusUnauthorized, Apply: withoutAuth},
	},
	{
		Method: http.MethodPut,
		Path:   "/admin/groups/{group_id}",
		Error:  &errorCase{Status: http.StatusUnauthorized, Apply: withoutAuth},
	},
	{
		Method: http.MethodGet,
		Path:   "/admin/enrollments",
		Error:  &errorCase{Status: http.StatusUnauthorized, Apply: withoutAuth},
	},
	{
		Method: http.MethodGet,
		Path:   "/admin/enrollments/export",
		Error:  &errorCase{Status: http.StatusUnauthorized, Apply: withoutAuth},
	},
	{
		Method: http.MethodGet,
		Path:   "/admin/stats/overview",
		Error:  &errorCase{Status: http.StatusUnauthorized, Apply: withoutAuth},
	},
}

func withoutAuth(request *apiRequest) {
	request.Token = ""
}

func eventNotFound(request *apiRequest) {
	request.PathValues["event_id"] = "nonexistent-event"
}

func mustContractPath(t *testing.T, method, path string) pathContract {
	t.Helper()
	for _, candidate := range contractPaths {
		if candidate.Method == method && candidate.Path == path {
			return candidate
		}
	}
	t.Fatalf("契约表缺少 %s %s", method, path)
	return pathContract{}
}

func newContractEngine(t *testing.T) http.Handler {
	t.Helper()
	gin.SetMode(gin.TestMode)
	return router.New(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		router.WithAuth(authhandler.New(contractLoginService{})),
		router.WithEvent(eventhandler.New(contractEventService{})),
		router.WithEnrollment(enrollmenthandler.New(contractEnrollmentService{}, contractEnrollmentService{}, contractEnrollmentService{})),
		router.WithPayment(paymenthandler.New(contractPaymentProvider{}, contractPaymentService{})),
		router.WithNotification(notificationhandler.New(contractNotificationService{})),
		router.WithAdmin(adminhandler.New(contractAdminService{})),
		router.WithTokenVerifier(contractTokenVerifier{}),
	)
}

// contractEnrollmentService 是 enrollment handler 的契约测试替身。
type contractEnrollmentService struct{}

func (contractEnrollmentService) Lock(_ context.Context, _, _ string) (*enrollmentsvc.LockResult, error) {
	return &enrollmentsvc.LockResult{
		Token:          "contract-lock-token",
		ExpiresAt:      time.Now().Add(15 * time.Minute),
		RemainingSlots: 1,
	}, nil
}

func (contractEnrollmentService) Submit(_ context.Context, userID string, input enrollmentsvc.SubmitInput) (*enrollmentsvc.SubmitResult, error) {
	now := time.Now()
	return &enrollmentsvc.SubmitResult{
		Enrollment: &model.Enrollment{
			ID:        "contract-enrollment",
			UserID:    userID,
			EventID:   "contract-event",
			GroupID:   input.GroupID,
			OrderID:   "contract-order",
			Status:    "pending_payment",
			FormData:  input.FormData,
			CreatedAt: now,
		},
		Order: &model.Order{
			ID:           "contract-order",
			EnrollmentID: "contract-enrollment",
			UserID:       userID,
			GroupID:      input.GroupID,
			Amount:       10000,
			Status:       "pending_payment",
			CreatedAt:    now,
		},
	}, nil
}

func (contractEnrollmentService) ListMine(_ context.Context, userID string) ([]model.Enrollment, error) {
	return []model.Enrollment{{
		ID:        "contract-enrollment",
		UserID:    userID,
		EventID:   "contract-event",
		GroupID:   "contract-group",
		OrderID:   "contract-order",
		Status:    "pending_payment",
		FormData:  map[string]any{"name": "contract", "phone": "13800138000"},
		CreatedAt: time.Now(),
	}}, nil
}

func (contractEnrollmentService) Detail(_ context.Context, userID, enrollmentID string) (*model.Enrollment, error) {
	return &model.Enrollment{
		ID:        enrollmentID,
		UserID:    userID,
		EventID:   "contract-event",
		GroupID:   "contract-group",
		OrderID:   "contract-order",
		Status:    "pending_payment",
		FormData:  map[string]any{"name": "contract"},
		CreatedAt: time.Now(),
	}, nil
}

// contractPaymentProvider 是 payment Provider 的契约测试替身。
type contractPaymentProvider struct{}

func (contractPaymentProvider) Prepay(_ context.Context, req paymentpkg.PrepayRequest) (*paymentpkg.PrepayResult, error) {
	return &paymentpkg.PrepayResult{
		PrepayID:  "contract-prepay",
		TimeStamp: "1700000000",
		NonceStr:  "contract-nonce",
		Package:   "prepay_id=contract-prepay",
		SignType:  "RSA",
		PaySign:   "contract-pay-sign",
	}, nil
}

func (contractPaymentProvider) VerifyNotify(_ context.Context, headers http.Header, _ []byte) (*paymentpkg.NotifyResult, error) {
	if headers.Get("Wechatpay-Signature") == "" {
		return nil, paymentpkg.ErrInvalidSignature
	}
	return &paymentpkg.NotifyResult{
		EventType:     "TRANSACTION.SUCCESS",
		OutTradeNo:    "contract-order",
		TransactionID: "contract-transaction",
		TradeState:    "SUCCESS",
		Amount:        paymentpkg.NotifyAmount{Total: 10000, Currency: "CNY"},
	}, nil
}

func (contractPaymentProvider) Refund(_ context.Context, req paymentpkg.RefundRequest) (*paymentpkg.RefundResult, error) {
	return &paymentpkg.RefundResult{
		Status:   paymentpkg.RefundStatusProcessing,
		RefundID: "contract-refund",
	}, nil
}

func (contractPaymentProvider) VerifyRefundNotify(_ context.Context, _ http.Header, _ []byte) (*paymentpkg.RefundNotifyResult, error) {
	return &paymentpkg.RefundNotifyResult{}, nil
}

// contractPaymentService 是 payment handler 的契约测试替身。
type contractPaymentService struct{}

func (contractPaymentService) PreparePrepay(_ context.Context, _, orderID string) (*paymentpkg.PrepayRequest, error) {
	return &paymentpkg.PrepayRequest{
		OutTradeNo:  orderID,
		Description: "赛事报名-" + orderID,
		AmountTotal: 10000,
		OpenID:      "contract-openid",
	}, nil
}

func (contractPaymentService) HandleNotify(_ context.Context, _ *paymentpkg.NotifyResult) error {
	return nil
}

// contractNotificationService 是 notification handler 的契约测试替身。
type contractNotificationService struct{}

func (contractNotificationService) Subscribe(context.Context, string, []string) error {
	return nil
}

// contractAdminService 是 admin handler 的契约测试替身。
type contractAdminService struct{}

func (contractAdminService) eventDetail(eventID string) *adminrepo.EventDetailData {
	now := time.Now()
	return &adminrepo.EventDetailData{
		Event: &model.Event{
			ID: eventID, Title: "契约赛事", CoverURL: "https://example.com/cover.png",
			StartTime: now, Location: "上海", Status: "registration_open",
			Description: "desc", Rules: "", CreatedAt: now, UpdatedAt: now,
		},
		Groups: []adminrepo.GroupWithRemaining{},
	}
}

func (contractAdminService) CreateEvent(context.Context, adminrepo.CreateEventParams) (*adminrepo.EventDetailData, error) {
	return contractAdminService{}.eventDetail("contract-event"), nil
}

func (contractAdminService) UpdateEvent(context.Context, adminrepo.UpdateEventParams) (*adminrepo.EventDetailData, error) {
	return contractAdminService{}.eventDetail("contract-event"), nil
}

func (contractAdminService) EventDetail(context.Context, string) (*adminrepo.EventDetailData, error) {
	return contractAdminService{}.eventDetail("contract-event"), nil
}

func (contractAdminService) CreateGroup(_ context.Context, params adminrepo.CreateGroupParams) (*adminrepo.GroupUpdateResult, error) {
	return &adminrepo.GroupUpdateResult{
		Group: &model.Group{
			ID: "contract-group", EventID: params.EventID, Name: params.Name,
			Price: params.Price, TotalSlots: params.TotalSlots, StartTime: params.StartTime,
			Status: "open", FormSchema: params.FormSchema,
			DisclaimerVersion: params.DisclaimerVersion,
		},
		RemainingSlots: params.TotalSlots,
	}, nil
}

func (contractAdminService) ListEnrollments(context.Context, adminrepo.EnrollmentFilter) (*adminrepo.EnrollmentPage, error) {
	return &adminrepo.EnrollmentPage{Items: []model.Enrollment{}, Total: 0}, nil
}

func (contractAdminService) StatsOverview(_ context.Context, eventID string) (*adminrepo.StatsOverviewData, error) {
	return &adminrepo.StatsOverviewData{EventID: eventID, Groups: []adminrepo.GroupStats{}}, nil
}

func (contractAdminService) UpdateGroup(_ context.Context, params adminrepo.UpdateGroupParams) (*adminrepo.GroupUpdateResult, error) {
	now := time.Now()
	return &adminrepo.GroupUpdateResult{
		Group: &model.Group{
			ID:                params.GroupID,
			EventID:           "contract-event",
			Name:              "契约组",
			Price:             10000,
			TotalSlots:        100,
			StartTime:         now,
			Status:            "open",
			FormSchema:        map[string]any{},
			DisclaimerVersion: "v1",
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		RemainingSlots: 99,
	}, nil
}

func (contractAdminService) Export(context.Context, string, adminrepo.ExportFilter) (*adminsvc.ExportResult, error) {
	return &adminsvc.ExportResult{
		Data:   []byte("contract-xlsx"),
		Rows:   0,
		Total:  0,
		Fields: []string{"enrollment_id"},
	}, nil
}

// contractTokenVerifier 是 JWT 中间件的契约测试替身。
type contractTokenVerifier struct{}

func (contractTokenVerifier) Parse(raw string) (*jwtx.Claims, error) {
	switch raw {
	case "contract-rider-token":
		return &jwtx.Claims{
			Role: "rider",
			RegisteredClaims: jwtlib.RegisteredClaims{
				Subject: "contract-rider",
			},
		}, nil
	case "contract-admin-token":
		return &jwtx.Claims{
			Role: "admin",
			RegisteredClaims: jwtlib.RegisteredClaims{
				Subject: "contract-admin",
			},
		}, nil
	default:
		return nil, jwtx.ErrInvalidToken
	}
}

// contractEventService 是 event handler 的契约测试替身，避免测试依赖数据库与 Redis。
type contractEventService struct{}

func (contractEventService) List(context.Context, string, int, int) (*eventsvc.ListResult, error) {
	start := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	return &eventsvc.ListResult{
		Events: []model.Event{{
			ID:        "contract-event",
			Title:     "契约赛事",
			CoverURL:  "https://example.com/cover.png",
			StartTime: start,
			Location:  "契约地点",
			Status:    "registration_open",
		}},
		MinPrices: map[string]int{"contract-event": 1000},
		Total:     1,
	}, nil
}

func (contractEventService) Detail(_ context.Context, eventID string) (*eventsvc.DetailResult, error) {
	if eventID == "nonexistent-event" {
		return nil, eventsvc.ErrEventNotFound
	}
	start := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	return &eventsvc.DetailResult{
		Event: &model.Event{
			ID:          eventID,
			Title:       "契约赛事",
			CoverURL:    "https://example.com/cover.png",
			StartTime:   start,
			Location:    "契约地点",
			Status:      "registration_open",
			Description: "契约赛事详情",
		},
		Groups: []eventsvc.GroupView{{
			Group: model.Group{
				ID:                "contract-group",
				EventID:           eventID,
				Name:              "契约组",
				Price:             1000,
				TotalSlots:        100,
				StartTime:         start,
				Status:            "open",
				FormSchema:        map[string]any{},
				DisclaimerVersion: "v1",
			},
			RemainingSlots: 42,
			Status:         "open",
		}},
	}, nil
}

// contractLoginService 是 auth handler 的契约测试替身，避免测试依赖微信网络。
type contractLoginService struct{}

func (contractLoginService) Login(_ context.Context, code string) (*authsvc.LoginResult, error) {
	if code == "invalid-code" {
		return nil, authsvc.ErrInvalidCode
	}
	return &authsvc.LoginResult{
		Token: "contract-token",
		User: &model.User{
			ID:        "contract-user",
			Nickname:  "契约用户",
			AvatarURL: "",
			Role:      "rider",
		},
	}, nil
}

func riderToken() string {
	if token := strings.TrimSpace(os.Getenv("CONTRACT_RIDER_TOKEN")); token != "" {
		return token
	}
	return "contract-rider-token"
}

func adminToken() string {
	if token := strings.TrimSpace(os.Getenv("CONTRACT_ADMIN_TOKEN")); token != "" {
		return token
	}
	return "contract-admin-token"
}

func tokenFor(op *apiOperation) string {
	if strings.HasPrefix(op.Path, "/admin") {
		return adminToken()
	}
	return riderToken()
}

// newValidRequest 按 OpenAPI 约束生成一个结构合法的请求。
//
// 路径参数、必填查询参数与请求体都从 schema 推导；字符串值可用
// CONTRACT_FIXTURE_<FIELD> 覆盖为真实夹具 ID（例如 CONTRACT_FIXTURE_EVENT_ID）。
func newValidRequest(t *testing.T, spec *openAPISpec, op *apiOperation) *apiRequest {
	t.Helper()

	request := &apiRequest{
		Method:     op.Method,
		Path:       op.Path,
		PathValues: make(map[string]string),
		Query:      make(url.Values),
	}

	for _, parameter := range spec.parameters(t, op) {
		if parameter.Name == "" {
			continue
		}
		value := fmt.Sprint(spec.sampleForSchema(parameter.Schema, parameter.Name, 0))
		switch parameter.In {
		case "path":
			request.PathValues[parameter.Name] = value
		case "query":
			if parameter.Required {
				request.Query.Set(parameter.Name, value)
			}
		}
	}

	if schema, ok := spec.requestSchema(t, op); ok {
		body, err := json.Marshal(spec.sampleForSchema(schema, "body", 0))
		if err != nil {
			t.Fatalf("生成 %s %s 请求体失败: %v", op.Method, op.Path, err)
		}
		request.Body = body
	}

	if spec.requiresBearer(op) {
		request.Token = tokenFor(op)
	}
	return request
}

func executeRequest(engine http.Handler, request *apiRequest) *httptest.ResponseRecorder {
	target := request.Path
	for name, value := range request.PathValues {
		target = strings.ReplaceAll(target, "{"+name+"}", url.PathEscape(value))
	}
	if len(request.Query) > 0 {
		target += "?" + request.Query.Encode()
	}

	var body io.Reader
	if request.Body != nil {
		body = bytes.NewReader(request.Body)
	}
	httpRequest := httptest.NewRequest(request.Method, apiPrefix+target, body)
	if request.Body != nil {
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	if request.Token != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+request.Token)
	}

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httpRequest)
	return recorder
}

// skipIfStub 在处理器仍是 501 占位实现时跳过断言。
//
// 去掉占位实现（不再返回 501 NOT_IMPLEMENTED）后，同一套断言自动开始生效。
func skipIfStub(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()

	if recorder.Code != http.StatusNotImplemented {
		return
	}
	var envelope struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil || envelope.Code != response.CodeNotImplemented {
		return
	}

	if os.Getenv("CONTRACT_STRICT") == "1" {
		t.Fatalf("接口仍为 501 占位实现（%s），CONTRACT_STRICT=1 要求契约断言必须生效", response.CodeNotImplemented)
	}
	t.Skip("接口仍为 501 占位实现（NOT_IMPLEMENTED），契约断言待业务实现后自动生效；设置 CONTRACT_STRICT=1 可改为失败")
}

// runPathContract 执行一个路径的四项契约检查。
func runPathContract(t *testing.T, method, path string) {
	t.Helper()

	spec := loadSpec(t)
	op := spec.operation(t, method, path)
	contract := mustContractPath(t, method, path)
	engine := newContractEngine(t)

	// 合法请求只发送一次，避免 POST 类接口产生重复副作用。
	var validResponse *httptest.ResponseRecorder
	if contract.SkipValidReason == "" {
		validResponse = executeRequest(engine, newValidRequest(t, spec, op))
	}

	t.Run("合法请求被接受", func(t *testing.T) {
		if contract.SkipValidReason != "" {
			t.Skip(contract.SkipValidReason)
		}
		skipIfStub(t, validResponse)
		if !spec.isSuccessStatus(op, validResponse.Code) {
			t.Fatalf("合法请求应返回 %v，实际 %d；响应体：%s",
				spec.successStatuses(op), validResponse.Code, truncate(validResponse.Body.Bytes()))
		}
	})

	t.Run("缺失必填字段被拒绝", func(t *testing.T) {
		if contract.SkipMissingReason != "" {
			t.Skip(contract.SkipMissingReason)
		}
		cases := spec.missingRequiredCases(t, op)
		if len(cases) == 0 {
			t.Skip("该操作没有可省略的必填请求字段（路径参数除外）")
		}

		for _, missing := range cases {
			missing := missing
			t.Run("缺失 "+missing.Name, func(t *testing.T) {
				request := newValidRequest(t, spec, op)
				if missing.Body != nil {
					request.Body = missing.Body
				}
				if missing.RemoveQuery != "" {
					request.Query.Del(missing.RemoveQuery)
				}
				recorder := executeRequest(engine, request)
				skipIfStub(t, recorder)
				if recorder.Code != http.StatusBadRequest && recorder.Code != http.StatusUnprocessableEntity {
					t.Fatalf("缺失必填字段 %s 应返回 400/422，实际 %d；响应体：%s",
						missing.Name, recorder.Code, truncate(recorder.Body.Bytes()))
				}
				spec.assertErrorEnvelope(t, recorder)
			})
		}
	})

	t.Run("响应包含所有 required 字段", func(t *testing.T) {
		if contract.SkipValidReason != "" {
			t.Skip(contract.SkipValidReason)
		}
		skipIfStub(t, validResponse)
		if !spec.isSuccessStatus(op, validResponse.Code) {
			t.Skipf("合法请求未被接受（状态码 %d），无法校验成功响应字段", validResponse.Code)
		}
		spec.assertRequiredResponse(t, op, validResponse.Body.Bytes())
	})

	t.Run("错误响应 code 在枚举范围内", func(t *testing.T) {
		if contract.Error == nil {
			t.Skip("OpenAPI 未定义可由客户端触发的错误响应")
		}
		request := newValidRequest(t, spec, op)
		if contract.Error.Apply != nil {
			contract.Error.Apply(request)
		}
		recorder := executeRequest(engine, request)
		spec.assertErrorResponse(t, recorder, contract.Error.Status)
	})
}

// missingCase 表示“省略一个必填输入”的请求变体。
type missingCase struct {
	Name        string
	Body        []byte
	RemoveQuery string
}

func (s *openAPISpec) missingRequiredCases(t *testing.T, op *apiOperation) []missingCase {
	t.Helper()

	var cases []missingCase

	if schema, ok := s.requestSchema(t, op); ok {
		fields := s.requiredFields(schema)
		if len(fields) > 0 {
			sample := s.sampleForSchema(schema, "body", 0)
			object, ok := sample.(map[string]any)
			if !ok {
				t.Fatalf("%s %s: 请求体 schema 不是 object，无法构造缺失字段用例", op.Method, op.Path)
			}
			for _, field := range fields {
				clone := make(map[string]any, len(object))
				for key, value := range object {
					clone[key] = value
				}
				delete(clone, field)
				body, err := json.Marshal(clone)
				if err != nil {
					t.Fatalf("%s %s: 生成缺失 %s 的请求体失败: %v", op.Method, op.Path, field, err)
				}
				cases = append(cases, missingCase{Name: field, Body: body})
			}
		}
	}

	for _, parameter := range s.parameters(t, op) {
		if parameter.In == "query" && parameter.Required {
			cases = append(cases, missingCase{Name: parameter.Name, RemoveQuery: parameter.Name})
		}
	}
	return cases
}

func (s *openAPISpec) assertRequiredResponse(t *testing.T, op *apiOperation, body []byte) {
	t.Helper()

	schema, ok := s.successJSONSchema(t, op)
	if !ok {
		t.Skip("该操作成功响应不是 JSON（没有 required 字段可校验）")
	}

	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("%s %s: 成功响应不是合法 JSON: %v；响应体：%s", op.Method, op.Path, err, truncate(body))
	}
	if problems := s.validateRequiredFields(t, schema, decoded); len(problems) > 0 {
		t.Fatalf("%s %s: 成功响应缺少 required 字段:\n  %s", op.Method, op.Path, strings.Join(problems, "\n  "))
	}
}

func (s *openAPISpec) assertErrorResponse(t *testing.T, recorder *httptest.ResponseRecorder, expectedStatus int) {
	t.Helper()

	skipIfStub(t, recorder)
	if recorder.Code != expectedStatus {
		t.Fatalf("错误场景应返回 %d，实际 %d；响应体：%s",
			expectedStatus, recorder.Code, truncate(recorder.Body.Bytes()))
	}
	s.assertErrorEnvelope(t, recorder)
}

func (s *openAPISpec) assertErrorEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()

	schema := s.errorResponseSchema(t)
	envelope := decodeJSONObject(t, recorder.Body.Bytes())
	if problems := s.validateRequiredFields(t, schema, envelope); len(problems) > 0 {
		t.Fatalf("错误响应缺少 required 字段:\n  %s", strings.Join(problems, "\n  "))
	}

	code := asString(envelope["code"])
	allowed := s.errorCodeEnum(t)
	if !slices.Contains(allowed, code) {
		t.Fatalf("错误码 %q 不在 ErrorResponse.code 枚举内: %v", code, allowed)
	}
}

// TestContractCoverage 保证 openapi.yaml 的每个路径都有对应测试骨架，且没有多余条目。
func TestContractCoverage(t *testing.T) {
	spec := loadSpec(t)

	declared := make(map[string]bool)
	for _, op := range spec.allOperations(t) {
		declared[op.Method+" "+op.Path] = true
	}

	covered := make(map[string]bool, len(contractPaths))
	for _, contract := range contractPaths {
		key := contract.Method + " " + contract.Path
		if covered[key] {
			t.Errorf("契约表中 %s 重复", key)
		}
		covered[key] = true
		if !declared[key] {
			t.Errorf("契约表包含 OpenAPI 未定义的路径 %s", key)
		}
	}

	for key := range declared {
		if !covered[key] {
			t.Errorf("OpenAPI 路径缺少契约测试骨架: %s", key)
		}
	}
}

// TestErrorCasesAreDocumented 保证每个错误场景的状态码都在对应 operation 中声明。
func TestErrorCasesAreDocumented(t *testing.T) {
	spec := loadSpec(t)

	for _, contract := range contractPaths {
		if contract.Error == nil {
			continue
		}
		op := spec.operation(t, contract.Method, contract.Path)
		if !spec.hasResponseStatus(op, contract.Error.Status) {
			t.Errorf("%s %s: 错误场景状态码 %d 未在 OpenAPI responses 中定义",
				contract.Method, contract.Path, contract.Error.Status)
		}
	}
}

// TestRequiredFieldValidatorDetectsNestedMissing 校验断言辅助本身能发现嵌套 required 字段缺失。
func TestRequiredFieldValidatorDetectsNestedMissing(t *testing.T) {
	spec := loadSpec(t)
	op := spec.operation(t, http.MethodPost, "/enrollments")
	schema, ok := spec.successJSONSchema(t, op)
	if !ok {
		t.Fatal("POST /enrollments 缺少成功响应 schema")
	}

	value := map[string]any{
		"enrollment": map[string]any{
			"id":         "enrollment-1",
			"event_id":   "event-1",
			"group_id":   "group-1",
			"user_id":    "user-1",
			"order_id":   "order-1",
			"created_at": dateTimeSample,
			// 故意缺少 status
		},
		"order": map[string]any{
			"id":            "order-1",
			"enrollment_id": "enrollment-1",
			"amount":        1,
			"status":        "pending_payment",
			"created_at":    dateTimeSample,
		},
	}

	problems := spec.validateRequiredFields(t, schema, value)
	for _, problem := range problems {
		if strings.Contains(problem, "enrollment.status") {
			return
		}
	}
	t.Fatalf("未检测到嵌套 required 字段缺失 enrollment.status，实际结果: %v", problems)
}

// TestErrorCodeEnumParsed 校验错误码枚举确实来自 openapi.yaml。
func TestErrorCodeEnumParsed(t *testing.T) {
	spec := loadSpec(t)
	allowed := spec.errorCodeEnum(t)

	for _, expected := range []string{"AUTH_INVALID_TOKEN", "PAY_SIGN_INVALID", "INTERNAL_ERROR"} {
		if !slices.Contains(allowed, expected) {
			t.Errorf("ErrorResponse.code 枚举缺少 %s，实际: %v", expected, allowed)
		}
	}
}
