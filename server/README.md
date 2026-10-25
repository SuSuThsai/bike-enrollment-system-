# bike-enroll 后端（Gin）

按 `docs/openapi.yaml` 逐模块实现。当前已完成 **模块 1：微信小程序登录**、
**模块 2：赛事列表与详情**、**模块 3：名额锁定**、**模块 4：提交报名**、
**模块 5：微信支付预下单**、**模块 6：支付回调**、**模块 7：免责协议签署记录**、
**模块 8：订阅消息授权与异步通知**、**模块 9：定时清理任务**、
**模块 10：对账任务与命令行**、**模块 11：后台管理接口**、
**模块 12：我的报名列表与详情**；OpenAPI 定义的 MVP 业务接口均已实现。

## 技术栈

- Go 1.26+
- Gin v1.10
- MySQL 8.0 + GORM v2
- Redis 7（AOF）
- JWT HS256（`golang-jwt/jwt/v5`）
- 微信小程序 `code2session`
- 微信支付 V3（JSAPI 下单、回调查验签/AES-GCM 解密）
- 后续接入：定时任务

## 目录结构

```text
server/
├── cmd/api/main.go                  # 入口：配置、MySQL、JWT、依赖装配、优雅退出
├── cmd/reconcile/main.go            # 手动执行一次全量对账
├── configs/README.md                # 证书/私钥获取与放置说明（证书本身不入库）
├── internal/
│   ├── config/                      # 环境变量加载
│   ├── model/                       # users/events/groups/locks/enrollments/orders/disclaimer 模型
│   ├── repository/
│   │   ├── mysql.go                 # MySQL/GORM 连接
│   │   ├── redis.go                 # Redis 连接
│   │   ├── user/                    # 用户仓储
│   │   ├── event/                   # 赛事仓储 + Redis 名额读取
│   │   ├── lock/                    # 锁记录仓储 + Redis Lua/NX 操作
│   │   ├── enrollment/              # 提交报名事务仓储
│   │   ├── payment/                 # 支付预下单/回调事务仓储
│   │   ├── notification/            # 订阅授权与通知数据仓储
│   │   └── admin/                   # 组别名额调整/报名导出仓储
│   ├── service/
│   │   ├── auth/                    # 微信登录业务逻辑
│   │   ├── event/                   # 赛事列表/详情业务逻辑
│   │   ├── enrollment/              # 名额锁定/提交报名业务逻辑 + 集成测试
│   │   ├── payment/                 # 预下单/回调业务逻辑 + 单元/集成测试
│   │   ├── notification/            # 订阅授权 + 异步通知队列
│   │   └── admin/                   # 组别调整/导出业务逻辑
│   ├── handler/
│   │   ├── auth/                    # 登录 HTTP handler + 单元测试
│   │   ├── event/                   # 赛事 HTTP handler
│   │   ├── enrollment/              # 名额锁定/提交报名 HTTP handler
│   │   ├── payment/                 # 支付预下单/回调 HTTP handler
│   │   ├── notification/            # 订阅授权 HTTP handler
│   │   └── admin/                   # 管理后台 HTTP handler
│   ├── pkg/jwt/                     # JWT 签发/校验
│   ├── pkg/lua/                     # lock.lua / rollback.lua + go:embed
│   ├── pkg/payment/                 # 支付 Provider 抽象：wechat / mock / factory
│   ├── pkg/wechat/                  # code2session 客户端
│   ├── pkg/wechat/pay/              # 微信支付 V3 客户端、回调查验签/解密
│   ├── pkg/wechat/subscribe/        # 微信订阅消息发送客户端
│   ├── job/                         # lock 过期/支付超时/对账定时任务
│   ├── middleware/                  # 日志、恢复、JWT 验签、管理员校验
│   ├── contract/                    # 基于 openapi.yaml 的契约测试骨架
│   └── router/                      # 路由分组装配
├── migrations/
│   ├── 000001_create_users.{up,down}.sql
│   ├── 000002_create_events_groups.{up,down}.sql
│   ├── 000003_create_enrollment_locks.{up,down}.sql
│   ├── 000004_create_enrollments_orders.{up,down}.sql
│   ├── 000005_add_payment_fields.{up,down}.sql
│   ├── 000006_create_notification_authorizations.{up,down}.sql
│   ├── 000007_add_timeout_fields.{up,down}.sql
│   └── 000008_create_refunds.{up,down}.sql
├── go.mod
└── README.md
```

## 本地启动

```bash
# 仓库根目录
docker compose up -d mysql redis

# 应用迁移（PowerShell，按文件名顺序）
Get-ChildItem server/migrations/*.up.sql | Sort-Object Name | ForEach-Object {
  Get-Content -Raw $_.FullName |
    docker compose exec -T mysql sh -c 'mysql -u"$MYSQL_USER" -p"$MYSQL_PASSWORD" "$MYSQL_DATABASE"'
}

# 设置最少环境变量
$env:JWT_SECRET = "<至少 32 字节的随机串>"
$env:WECHAT_APPID = "<小程序 appid>"
$env:WECHAT_SECRET = "<小程序 secret>"

cd server
go run ./cmd/api
```

也可以直接使用全栈 Compose（根目录 `.env` 需配置真实微信参数）：

```bash
docker compose up -d --build
```

环境变量：

| 变量 | 默认值 | 说明 |
|---|---|---|
| `APP_ENV` | `development` | `production` 时切换 Gin Release 模式 |
| `HTTP_ADDR` | `:8080` | HTTP 监听地址 |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `MYSQL_HOST` / `MYSQL_PORT` | `127.0.0.1` / `3306` | MySQL 地址 |
| `MYSQL_USER` / `MYSQL_PASSWORD` | `bike` / 无默认值 | MySQL 账号；密码必须在 `.env` 中显式设置，缺失即连接失败 |
| `MYSQL_DATABASE` | `bike_enroll` | MySQL 库名 |
| `REDIS_ADDR` | `127.0.0.1:6379` | Redis 地址 |
| `REDIS_PASSWORD` / `REDIS_DB` | 空 / `0` | Redis 密码与库号 |
| `JWT_SECRET` | 无 | 必填，至少 32 字节；缺失或过短时服务拒绝启动 |
| `JWT_EXPIRE_HOURS` | `168` | JWT 有效期 |
| `PAYMENT_MODE` | `wechat` | 支付提供者：`mock`（本地联调，不验签）或 `wechat`（真实微信支付）；空值按 `wechat` 处理 |
| `MOCK_PAY_SECRET` | 空 | 仅 `PAYMENT_MODE=mock` 的 `/internal/mock-pay/*` 使用；为空时接口一律 403 |
| `LOCK_TTL_MINUTES` | `15` | 名额锁有效期 |
| `PAY_TIMEOUT_MINUTES` | `15` | 支付超时取消阈值 |
| `WECHAT_APPID` / `WECHAT_SECRET` | 空 | 小程序登录/订阅消息凭据；为空时登录返回 500，通知发送跳过 |
| `WECHAT_TPL_PAY_RESULT` | 空 | 支付结果订阅消息模板 ID |
| `WECHAT_TPL_ENROLL_RESULT` | 空 | 报名成功订阅消息模板 ID |
| `WECHAT_PAY_MCHID` | 空 | 微信支付商户号 |
| `WECHAT_PAY_SERIAL_NO` | 空 | 商户 API 证书序列号 |
| `WECHAT_PAY_PRIVATE_KEY_PATH` | `./configs/apiclient_key.pem` | 商户 API 私钥路径 |
| `WECHAT_PAY_NOTIFY_URL` | 空 | 支付回调地址 |
| `WECHAT_PAY_API_V3_KEY` | 空 | APIv3 密钥（32 字节），回调解密用 |
| `WECHAT_PAY_PLATFORM_CERT_PATH` | `./configs/wechatpay_platform_cert.pem` | 微信支付平台证书，回调查验签名用 |

## 登录接口

`POST /api/v1/auth/wechat-login`

1. 接收 `wx.login` 的 `code`；
2. 调用微信 `code2session` 换取 `openid`；
3. 按 `openid` 查询用户，不存在则创建（默认昵称“微信用户”，角色 `rider`）；
4. 签发 JWT，响应 `{ token, user }`。

错误映射：

- 缺少/非法 `code`：`400 INVALID_ARGUMENT`
- 微信 code 无效或已使用：`400 AUTH_INVALID_CODE`
- 微信接口或内部依赖异常：`500 INTERNAL_ERROR`

JWT 声明包含 `sub`（用户 ID）、`role`、`iat`、`exp`。JWT 验签中间件已启用：受保护路由会解析
token、写入 `auth.user_id` / `auth.role`，过期 token 返回 `401 AUTH_EXPIRED`。

## 赛事接口

- `GET /events`：公开分页列表，`page` 默认 1，`page_size` 默认 20、最大 100；返回
  `{ items, total }`，`min_price` 为组别最低价。
- `GET /events/{event_id}`：公开详情，返回赛事与组别。
- `groups[].remaining_slots` 只从 Redis `group:{group_id}:remaining` 读取；key 缺失或值为负
  时返回 500，**不回退**到 `groups.total_slots`。
- `groups[].status` 中 `sold_out` 由 `remaining_slots == 0` 推导，不单独落库。

## 名额锁定

`POST /api/v1/enrollments/lock`（需 JWT）

1. `SET lock:{user_id}:{group_id} 1 NX PX 15000` 做用户级串行化；失败返回 `429 RATE_LIMITED`；
2. 通过 `pkg/lua/lock.lua`（`go:embed`）原子判断 `group:{group_id}:remaining > 0` 并 `DECR`；
3. 成功后插入 `enrollment_locks`（`status=active`，`expires_at=now+15min`，UUID v4 token）；
4. MySQL 插入失败时执行 `rollback.lua`：以 `lock:rollback:{lock_token}` 做幂等键，同一 token
   重复回补只 `INCR` 一次；
5. 返回 `{ lock_token, expires_at, remaining_slots }`。

锁定接口错误码：`GROUP_NOT_FOUND`、`EVENT_NOT_OPEN`、`GROUP_CLOSED`、`GROUP_SOLD_OUT`、
`RATE_LIMITED`、`INVALID_ARGUMENT`、`INTERNAL_ERROR`。

## 提交报名

`POST /api/v1/enrollments`（需 JWT）

在单个数据库事务内：

1. `UPDATE enrollment_locks SET status='consumed' WHERE token=? AND user_id=? AND group_id=?
   AND status='active' AND expires_at > NOW(3)`；
2. `affected_rows=0` 时区分 `LOCK_INVALID` / `LOCK_EXPIRED` / `LOCK_ALREADY_CONSUMED`；
3. 插入 `enrollments`（`pending_payment`）、`orders`（`pending_payment`，金额取 `group.price`）
   与 `disclaimer_signatures`（仅 INSERT）；
4. 提交成功后 lock 为 `consumed`，本接口不扣减、不回补 Redis。

`disclaimer_version` 必须与 `groups.disclaimer_version` 一致；同一用户同一组别重复报名由
`uk_enrollments_user_group` 唯一索引兜底，返回 `ENROLLMENT_DUPLICATE`。成功返回 201
`{ enrollment, order }`。

### 免责协议签署记录

`disclaimer_signatures` 随提交报名事务写入，字段包含 `user_id`、`enrollment_id`、
`disclaimer_version`、`signed_at`、`client_ip`、`user_agent`（另有 id/group_id/created_at）。
业务代码只有 `tx.Create` 路径，不存在 UPDATE/DELETE；模型不含 `updated_at`，表保持只增不改。

## 我的报名

- `GET /api/v1/enrollments/my`：返回当前用户全部报名，按 `created_at` 倒序；
  `form_data` 中身份证、手机号等敏感字段默认脱敏为 `***`。
- `GET /api/v1/enrollments/{enrollment_id}`：仅报名本人可读；不存在或不属于当前用户统一返回
  `404 ENROLLMENT_NOT_FOUND`。
- 两个接口均为只读，不修改数据库，也不访问 Redis。

## 支付提供者（Provider）

`internal/pkg/payment` 定义统一接口：

```go
type Provider interface {
    Prepay(ctx context.Context, req PrepayRequest) (*PrepayResult, error)
    VerifyNotify(ctx context.Context, headers http.Header, body []byte) (*NotifyResult, error)
}
```

由 `PAYMENT_MODE` 选择实现：

- `wechat`：`wechat_provider.go`，调用微信支付 V3 JSAPI 统一下单，回调使用平台证书验签 + APIv3 密钥解密；
- `mock`：`mock_provider.go`，Prepay 返回固定格式假参数，VerifyNotify 只做 `json.Unmarshal`，不验签；
- 空值按 `wechat` 处理，避免生产环境漏配时静默降级为 mock。

handler 只依赖 `Provider` 接口，内部没有 `if mock` 分支：回调先 `VerifyNotify`，成功后统一调用
`payment.Service.HandleNotify`；Mock 模式同样执行 `SELECT ... FOR UPDATE`、状态判断、金额校验和
Order/Enrollment 同事务迁移，不跳过任何一步。

Mock 回调体直接使用 `NotifyResult` JSON：

```json
{
  "event_type": "TRANSACTION.SUCCESS",
  "out_trade_no": "<Order.id>",
  "transaction_id": "mock-transaction-1",
  "trade_state": "SUCCESS",
  "amount": { "total": 1, "currency": "CNY" }
}
```

本地 mock 联调：

```dotenv
PAYMENT_MODE=mock
```

> Mock 模式的 `prepay_id` / `paySign` 不是微信真实签名，不能用于真实支付；上线前必须切换为 `wechat`
> 并补齐商户号、证书与 APIv3 密钥。
>
> 启动硬保护：`APP_ENV=production/prod` 且 `PAYMENT_MODE=mock` 时，`payment.NewProvider`
> 返回错误，API 进程直接退出，不会带 mock 支付能力启动。

### Mock 支付触发入口

仅当 `PAYMENT_MODE=mock` 时注册，且不在 `/api/v1` 下，避免进入对外契约：

```text
POST /internal/mock-pay/:order_id/success
POST /internal/mock-pay/:order_id/fail
```

两个接口都必须携带 `X-Mock-Secret: <MOCK_PAY_SECRET>`，否则返回 403 `AUTH_FORBIDDEN`；
`MOCK_PAY_SECRET` 为空时一律拒绝。

- `success`：读取 order.amount，构造 `TRANSACTION.SUCCESS` + 随机 `transaction_id` + 正确金额，
  调用 `payment.Service.HandleNotify`，完整执行状态迁移；
- `fail`：金额构造为 `order.amount + 1`，同样调用 `HandleNotify`，用于验证
  `PAY_AMOUNT_MISMATCH` 告警且不迁移状态。

```powershell
curl.exe -X POST http://localhost:8080/internal/mock-pay/<order_id>/success `
  -H "X-Mock-Secret: <MOCK_PAY_SECRET>"
```

## 微信支付预下单

`POST /api/v1/payments/prepay`（需 JWT）

1. 校验订单存在、属于当前用户；
2. 仅 `orders.status=pending_payment` 可调用，否则返回 `ORDER_STATUS_INVALID`；
3. 以 `order.id` 作为 `out_trade_no`，金额取 `order.amount`，`payer.openid` 取用户 `openid`；
   订单号使用 32 位无连字符 UUID，满足微信 `out_trade_no` 6–32 字符限制；
4. 调用微信支付 V3 JSAPI 统一下单，获取 `prepay_id`；
5. 使用商户私钥生成小程序 `wx.requestPayment` 参数：
   `{ timeStamp, nonceStr, package, signType: "RSA", paySign }`。

本接口不修改 Order/Enrollment 状态，也不操作 Redis。微信支付配置缺失时服务仍可启动，但预下单返回 500。

## 微信支付回调

`POST /api/v1/payments/notify`（公开，微信支付 V3 回调）

1. 先使用平台证书验签，失败返回 `401 PAY_SIGN_INVALID`，不访问数据库；
2. 使用 APIv3 密钥 AES-256-GCM 解密 `resource`；
3. 在单个事务内 `SELECT orders ... FOR UPDATE`；
4. 仅 `status=pending_payment` 时迁移：
   - 金额一致：Order → `paid`，Enrollment → `paid`，写 `transaction_id`/`paid_at`；
   - 金额不一致：回滚，不迁移，记录 `PAY_AMOUNT_MISMATCH` 告警，仍返回 200；
   - 重复回调：直接提交，不更新任何字段，返回 200；
5. 回调不执行任何 Redis `INCR`/`DECR`；
6. 成功及重复/金额不符/未知订单均返回 `200 {"code":"SUCCESS"}`。

## 订阅消息与异步通知

`POST /api/v1/notifications/subscribe`（需 JWT）

- 对上报的模板 ID 去重后写入 `notification_authorizations`，状态 `available`；
- 记录 `user_id`、`template_id`、`created_at`、`consumed_at`。

支付回调事务提交后，支付 service 只做非阻塞投递；通知 service 的 worker 异步执行：

1. 读取订单/报名/用户/赛事/组别数据；
2. 原子 `UPDATE ... SET status='consumed' ... WHERE status='available' ORDER BY created_at LIMIT 1` 消费一条授权；
3. 无未消费授权则跳过（不降级短信，避免额外依赖）；
4. 调用微信订阅消息接口，失败仅记录日志，不影响支付回调响应。

通知发送绝不在报名/支付事务内同步执行；队列满时直接丢弃并记录日志。
当前“报名成功”模板数据按 `thing1`（赛事）、`time2`（支付时间）、`thing3`（组别）、
`character_string4`（订单号）组装，`WECHAT_TPL_ENROLL_RESULT` 需配置字段匹配的模板。

## 定时任务

主进程启动后立即执行一轮；lock 过期/支付超时每分钟执行，对账每 5 分钟执行。

- **lock 过期释放**：
  - `SELECT token, group_id FROM enrollment_locks WHERE status='active' AND expires_at < NOW(3)
    ORDER BY expires_at LIMIT 200 FOR UPDATE SKIP LOCKED`；
  - 逐条事务 `UPDATE enrollment_locks SET status='expired' WHERE token=? AND status='active'`；
  - 仅 `affected_rows=1` 时 `INCR group:{group_id}:remaining`。
- **支付超时**：
  - 查询创建超过 `PAY_TIMEOUT_MINUTES` 的 `pending_payment` 订单，加 `FOR UPDATE SKIP LOCKED`；
  - 逐条事务将 Order 置 `expired`；
  - 仅订单 `affected_rows=1` 时同步 Enrollment → `cancelled`；
  - 完成后再 `INCR group:{group_id}:remaining`。
- **对账**：
  - 完整等式（`state-machine.md` 第 8 节）：
    `Redis remaining + active locks + pending_payment + paid/confirmed = total_slots`；
  - 不一致时 ERROR 日志输出 `alert=true`、`RECONCILIATION_MISMATCH`、Redis 值/是否存在、
    活跃 lock、待支付、已支付/已确认、total_slots、difference；
  - 只读，不修改 Redis/MySQL。

定时任务使用 `SET job:{name}:lock NX PX 60000` 单实例锁，任务结束通过
`job_unlock.lua` 校验 token 后释放。只有获得任务锁的实例才会扫描。

手动对账：

```bash
cd server
go run ./cmd/reconcile
```

命令输出每个 group 的计数，存在不一致时退出码为 1；`job:reconcile:lock` 被占用时也会退出 1。

说明：`enrollment_locks` 主键是 `token`，没有 `id` 列，因此任务以 `token` 作为行标识。

## 后台管理接口

- `POST /admin/events`：创建赛事，初始状态 `registration_open`，返回 EventDetail。
- `PUT /admin/events/{event_id}`：更新赛事字段，返回最新 EventDetail。
- `POST /admin/groups`：先插入 MySQL，再初始化
  `group:{group_id}:remaining = total_slots`；Redis 失败时删除 MySQL 记录。
- `PUT /admin/groups/{group_id}`：
  - 事务内 `SELECT groups ... FOR UPDATE` 并计算 `delta`；
  - Lua 原子执行 `remaining = remaining + delta`，为负返回 `GROUP_SOLD_OUT`；
  - Redis 成功后再更新 MySQL；MySQL 失败按相反 delta 回补 Redis；
  - 响应返回最新 `total_slots` 和 `remaining_slots`。
- `GET /admin/enrollments`：支持 `event_id`/`group_id`/`status` 筛选与 `page`/`page_size`
  分页；`form_data` 中身份证、手机号、体检等敏感字段默认脱敏为 `***`。
- `GET /admin/enrollments/export`：
  - 需要 `event_id`，可选 `group_id`、`fields`；
  - 查询 `LIMIT 10001`，只导出前 10000 条，超出时日志标记 `truncated=true`；
  - 生成 xlsx 文件流，Content-Type 为
    `application/vnd.openxmlformats-officedocument.spreadsheetml.sheet`；
  - 结构化审计日志记录 admin_id、event_id、group_id、rows、total、truncated。
- `GET /admin/stats/overview`：按 group 返回 `total_slots`、Redis `remaining_slots`、
  active lock、pending_payment、paid/confirmed 数，口径与对账等式一致。
- 所有 `/admin` 路由均挂载 `RequireAuth + RequireAdmin`；rider token 在进入 handler 前返回
  `403 AUTH_FORBIDDEN`。

## 测试

```bash
cd server
go test ./...
go test ./internal/handler/auth -v
go test ./internal/handler/enrollment -v
go test ./internal/handler/payment -v
go test ./internal/service/event -v
go test ./internal/service/payment -v
go test ./internal/service/notification -v
go test ./internal/service/admin -v
go test ./internal/pkg/lua -v
go test ./internal/router -v
go test ./internal/pkg/wechat/pay -v
go test ./internal/pkg/wechat/subscribe -v
go test ./internal/repository/enrollment -v

# 集成测试：需要本地 MySQL/Redis（Compose）且已执行 000003~000008 迁移
go test -tags=integration ./internal/service/enrollment -v
go test -tags=integration ./internal/service/payment -v
go test -tags=integration ./internal/service/notification -v
go test -tags=integration ./internal/service/admin -v
go test -tags=integration ./internal/job -v
go test -tags=integration ./tests/integration -run '^TestIntegrationMyEnrollments' -v
go test -tags=integration ./tests/integration -run '^TestMockPay' -v
go test -tags=integration ./tests/integration -run '^TestMockRefund' -v

# 手动对账（每天/需要时执行）
go run ./cmd/reconcile
```

`internal/handler/auth/handler_test.go` 使用 fake 微信客户端与 fake 用户仓储，覆盖新用户创建、
老用户复用、code 无效 400、缺少 code 400，并校验 JWT 声明与过期时间。

`internal/service/event/service_test.go` 覆盖 Redis 有名额时直接返回、Redis key 缺失时返回
`ErrRemainingUnavailable`、负剩余拒绝，以及 `sold_out` 推导。

`internal/service/enrollment/integration_test.go` 覆盖 100 用户抢 50 名额、已支付 1 个名额后
20 用户并发锁剩余 4 个名额（`paid + active = 5 <= total_slots`）、同一用户重复锁定
返回 `RATE_LIMITED`、MySQL 插入失败时 Redis 名额回补、同一 `lock_token` 重复回补不虚增；
提交报名覆盖 lock 过期/已消费、正常创建 Enrollment+Order、免责版本不一致、重复报名，以及
提交接口不修改 Redis。`internal/repository/enrollment/disclaimer_test.go` 使用 sqlmock 验证
签署记录只执行 INSERT，且模型没有 `updated_at` 更新字段。
`internal/handler/enrollment/handler_test.go` 覆盖“我的报名”列表脱敏、空列表、未登录、详情
本人可读和 404；`tests/integration/my_enrollments_test.go` 验证列表按用户隔离、详情仅本人可读。

`internal/service/payment/service_test.go` 使用 fake 仓储与 fake 微信支付客户端，覆盖订单状态
校验、订单归属校验、`out_trade_no`/金额/openid/回调地址组装，以及支付参数返回。
`internal/pkg/wechat/pay/client_test.go` 与 `notify_test.go` 覆盖 V3 请求签名、`paySign`、
回调平台证书验签和 APIv3 解密。

`internal/service/payment/notify_integration_test.go` 覆盖首次回调迁移、重复回调幂等、
金额不符不迁移并告警、验签失败不触库，以及回调与支付超时任务并发时只有一方成功迁移。
`internal/handler/payment/handler_test.go` 校验回调成功响应 `{"code":"SUCCESS"}` 与验签失败 401。

`internal/service/notification/service_test.go` 覆盖模板去重、异步发送、无授权跳过以及发送
失败仅记录日志；`integration_test.go` 覆盖授权写入/原子消费和通知数据关联查询。
`internal/pkg/wechat/subscribe/client_test.go` 覆盖 access_token 缓存与订阅消息发送请求。

`internal/job/integration_test.go` 覆盖 lock 过期回补一次、重复执行不重复回补、任务单实例锁、
支付超时任务与支付回调并发时只有一方成功迁移，以及对账等式不一致时的详细告警。

`internal/service/admin/service_test.go` 验证导出行数硬上限 10000、非法导出字段和非法
`total_slots`；`integration_test.go` 验证名额差值的 Redis/MySQL 同步、负剩余拒绝、后台创建
赛事/组别、名单分页脱敏和统计口径。`internal/pkg/lua/group_slots_test.go` 使用 miniredis 验证
名额差额 Lua 的原子性、负剩余拒绝和 key 缺失分支。
`internal/router/router_test.go` 遍历全部 admin 路由，验证 rider token 在进入 handler 前返回
`403 AUTH_FORBIDDEN`。

`internal/contract` 是基于冻结 OpenAPI 的契约测试骨架；已接入 auth / events / enrollment lock /
submit / list / detail / prepay / notifications subscribe 以及全部 admin 接口的 fake service 与
JWT 测试替身，无法构造合法请求的接口会 skip，设置 `CONTRACT_STRICT=1` 可把 skip 升级为失败。
支付回调因依赖 V3 签名夹具，契约测试的合法请求断言仍 skip；真实 provider 的 HTTP 回调由
`tests/integration/wechat_notify_test.go` 覆盖。

## 待实现

1. 短信降级、CORS、限流与其余迁移。
