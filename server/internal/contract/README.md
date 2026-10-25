# 契约测试骨架

基于 `docs/openapi.yaml` 的 HTTP 契约测试骨架，只做四类检查，不实现任何业务逻辑：

1. **合法请求是否被接受**：按 schema 生成合法请求，断言返回该 operation 声明的 2xx 状态码。
2. **缺失必填字段是否被拒绝**：逐个省略请求体 / 必填查询参数中的必填字段，断言返回 400/422，且错误体结构完整。
3. **响应是否包含所有 required 字段**：递归校验成功响应（含 `allOf`、`$ref`、数组元素、嵌套对象）是否包含 schema 声明的全部 `required` 字段。
4. **错误响应 code 是否在枚举范围内**：断言 `ErrorResponse.code` 的取值落在 `components.schemas.ErrorResponse.code.enum` 中。

`required` 列表、错误码枚举、成功状态码、样例值全部在运行时从 `docs/openapi.yaml` 读取，不在测试里重复维护；契约变更后断言自动跟随。

## 覆盖范围

| Tag | Method & Path | 测试入口 |
|---|---|---|
| auth | `POST /auth/wechat-login` | `TestContractAuthWechatLogin` |
| events | `GET /events` | `TestContractEventsList` |
| events | `GET /events/{event_id}` | `TestContractEventDetail` |
| enrollments | `POST /enrollments/lock` | `TestContractEnrollmentLock` |
| enrollments | `POST /enrollments` | `TestContractEnrollmentSubmit` |
| enrollments | `GET /enrollments/my` | `TestContractEnrollmentListMine` |
| enrollments | `GET /enrollments/{enrollment_id}` | `TestContractEnrollmentDetail` |
| payments | `POST /payments/prepay` | `TestContractPaymentPrepay` |
| payments | `POST /payments/notify` | `TestContractPaymentNotify` |
| notifications | `POST /notifications/subscribe` | `TestContractNotificationSubscribe` |
| admin | `POST /admin/events` | `TestContractAdminCreateEvent` |
| admin | `PUT /admin/events/{event_id}` | `TestContractAdminUpdateEvent` |
| admin | `POST /admin/groups` | `TestContractAdminCreateGroup` |
| admin | `PUT /admin/groups/{group_id}` | `TestContractAdminUpdateGroup` |
| admin | `GET /admin/enrollments` | `TestContractAdminListEnrollments` |
| admin | `GET /admin/enrollments/export` | `TestContractAdminExportEnrollments` |
| admin | `GET /admin/stats/overview` | `TestContractAdminStatsOverview` |

`TestContractCoverage` 双向校验：OpenAPI 的每个 path 都有测试入口，测试表也不能包含 OpenAPI 未定义的 path。
`TestErrorCasesAreDocumented` 校验每个错误场景的状态码确实在对应 operation 的 `responses` 中声明。

## 文件结构

| 文件 | 职责 |
|---|---|
| `openapi_test.go` | 加载 OpenAPI、`$ref`/`allOf` 展开、required 字段收集与递归校验、请求样例生成 |
| `contract_test.go` | 路径测试表、httptest 执行器、四项检查的公共流程、覆盖率与辅助函数自测 |
| `spec_quality_test.go` | 契约自身质量：operationId 唯一、必填输入声明 400、`$ref` 可解析、4xx/5xx 统一错误信封、Order/Enrollment 退款状态枚举一致性 |
| `*_test.go`（按 tag 拆分） | 每个 path 一个入口函数，便于按 tag 单独运行 |

## 运行

```bash
cd server

# 只跑契约骨架
go test ./internal/contract -v

# 只跑某个 tag
go test ./internal/contract -run 'TestContractAdmin' -v

# 全量
go test ./...
```

## 当前状态

OpenAPI 定义的 MVP 业务接口已全部实现，契约测试默认执行合法请求、错误码与 required 字段断言；
仅依赖微信 V3 签名夹具的支付回调合法请求仍 `skip`，真实 provider 的 HTTP 回调由
`tests/integration/wechat_notify_test.go` 覆盖。

保留 `CONTRACT_STRICT=1` 作为 CI 兜底，把任何残留的 501 占位响应从 `skip` 升级为失败：

```bash
CONTRACT_STRICT=1 go test ./internal/contract
```

## 环境变量

| 变量 | 用途 |
|---|---|
| `CONTRACT_STRICT` | 设为 `1` 时，把 501 占位响应的 skip 升级为失败 |
| `CONTRACT_RIDER_TOKEN` | 普通用户 JWT；鉴权落地后必须提供，否则受保护接口的合法请求 / 缺字段用例会得到 401 |
| `CONTRACT_ADMIN_TOKEN` | `role=admin` 的 JWT；admin 接口使用 |
| `CONTRACT_FIXTURE_<FIELD>` | 覆盖按字段名生成的夹具值，例如 `CONTRACT_FIXTURE_EVENT_ID=evt_123`、`CONTRACT_FIXTURE_CODE=wx_code` |

未设置 token 时使用固定占位串；当前占位中间件只校验 Bearer 格式，因此不影响 skip 逻辑。

路径参数、必填查询参数和请求体都由 schema 推导。字符串默认值为 `contract-<字段名>`，
数字默认 `1`，`date-time` 使用固定 RFC3339 值。默认值只保证**结构合法**，不保证在数据库中真实存在；
需要真实数据时用 `CONTRACT_FIXTURE_<FIELD>` 注入。

## 已知边界

- **校验失败错误码**：契约已统一为 `400 INVALID_ARGUMENT`，所有带请求体或必填查询参数的操作都声明了 400；
  骨架对缺失必填字段接受 `400/422`，并校验错误码必须在枚举内。
- **`POST /payments/notify` 的成功请求**依赖微信 V3 签名头与平台证书；契约已定义四个 `Wechatpay-*` 请求头与
  回调信封 schema，但测试环境没有私钥，合法请求用例仍以明确原因 skip；接入签名夹具或联调环境后，
  在 `contractPaths` 中去掉 `SkipValidReason` 即可启用。
- **`POST /auth/wechat-login` 的合法请求**需要真实 `wx.login` code，用 `CONTRACT_FIXTURE_CODE` 注入。
- **`GET /admin/enrollments/export`** 成功响应是 xlsx 二进制，没有 JSON `required` 字段，第 3 项检查会 skip。
- **`GET /events`** 只声明了 500，没有客户端可触发的 4xx，错误码检查会 skip。
- **写接口**（lock/submit/admin 创建或更新）会产生副作用，目前仅发送一次合法请求；真实联调时应对接隔离数据库与夹具，
  并在用例间清理数据，避免重复报名、名额占用等状态互相影响。
- **`PUT /admin/events/{event_id}`、`PUT /admin/groups/{group_id}`** 的 requestBody 没有 `required` 字段，
  因此“缺失必填字段”检查会 skip；这是契约现状，不是测试遗漏。

## 新增路径时

1. 在 `contractPaths` 增加一条记录（含可选的错误场景 / `SkipValidReason`）。
2. 在对应 tag 的 `*_test.go` 中增加一个 `TestContractXxx`，调用 `runPathContract`。
3. 跑 `TestContractCoverage`，确认双向覆盖没有遗漏。
