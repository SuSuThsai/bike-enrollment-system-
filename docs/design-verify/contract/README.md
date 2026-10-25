# 契约测试骨架（冻结快照）

本目录是 `docs/openapi.yaml` 契约测试骨架在 **2026-10-25** 冻结时的快照。

- **可执行权威版本**：`server/internal/contract/`，随 `server` 模块运行；
- **本目录**：用于设计评审与归档阅读，`*_test.go` 内容与权威版本一致，本 README 为归档说明。

## 为什么有两份

契约测试需要导入 `server/internal/router`、`server/internal/response` 等内部包，
只能在 `server` 模块内编译运行。`docs/` 下没有独立 `go.mod`，因此这里只保留快照；
修改测试后请同步复制，避免归档与实现漂移。

```bash
# 在仓库根目录，重新归档快照
cp server/internal/contract/*_test.go docs/design-verify/contract/
```

## 运行

```bash
cd server

go test ./internal/contract -v

# 只跑契约自身质量检查
go test ./internal/contract -run 'TestOperationIDs|TestInputOperations|TestAllLocalRefs|TestErrorResponses' -v

# 接口仍是 501 占位时，把 skip 升级为失败
CONTRACT_STRICT=1 go test ./internal/contract
```

## 文件职责

| 文件 | 职责 |
|---|---|
| `contract_test.go` | 路径表、httptest 执行器、四项检查主流程、覆盖率校验 |
| `openapi_test.go` | 加载 OpenAPI、`$ref`/`allOf` 展开、required 字段递归校验、请求样例生成 |
| `spec_quality_test.go` | operationId 唯一、必填输入声明 400、`$ref` 可解析、4xx/5xx 统一错误信封、退款状态枚举一致性 |
| `auth_test.go` / `events_test.go` / `enrollments_test.go` / `payments_test.go` / `notifications_test.go` / `admin_test.go` | 按 tag 拆分的入口函数 |

## 覆盖

OpenAPI 的 17 个操作全部有入口，`TestContractCoverage` 做双向校验
（每个 path 都有测试，测试表也不能包含未定义 path）。

| Tag | 操作 |
|---|---|
| auth | `POST /auth/wechat-login` |
| events | `GET /events`、`GET /events/{event_id}` |
| enrollments | `POST /enrollments/lock`、`POST /enrollments`、`GET /enrollments/my`、`GET /enrollments/{enrollment_id}` |
| payments | `POST /payments/prepay`、`POST /payments/notify` |
| notifications | `POST /notifications/subscribe` |
| admin | `POST /admin/events`、`PUT /admin/events/{event_id}`、`POST /admin/groups`、`PUT /admin/groups/{group_id}`、`GET /admin/enrollments`、`GET /admin/enrollments/export`、`GET /admin/stats/overview` |

## 断言口径

1. 合法请求被接受：按 schema 生成请求（路径参数、必填查询参数、必填请求体字段），断言返回该操作声明的 2xx；
2. 缺失必填字段被拒绝：逐个省略，断言 400/422，且错误体含 `code`、`message`、`request_id`；
3. 成功响应包含全部 `required` 字段：递归校验对象、数组、`allOf` 与 `$ref`；
4. 错误响应 `code` 在 `ErrorResponse.code` 枚举内。

`required`、错误码枚举、成功状态码和样例值都在运行时从 `docs/openapi.yaml` 读取，
契约变更后断言自动跟随，不在测试里重复维护。

## 已知边界

- OpenAPI 接口已全部实现；`CONTRACT_STRICT=1` 用于把任何仍返回 501 的接口从 `skip` 升级为失败。
- 支付回调合法请求依赖微信平台私钥签名，契约测试当前 skip；真实 provider 的 HTTP 回调由
  `server/tests/integration/wechat_notify_test.go` 覆盖。
- `spec_quality_test.go` 会校验 Order/Enrollment 状态枚举包含退款状态机要求的全部状态。
- 导出接口成功响应是 xlsx 二进制，没有 JSON `required` 字段，第 3 项检查 skip。
- 契约快照不可直接在 `docs/` 下运行，原因见上文“为什么有两份”。
