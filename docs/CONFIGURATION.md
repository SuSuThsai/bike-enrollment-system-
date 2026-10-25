# 环境变量配置说明

本文逐个说明本项目用到的环境变量：含义、是否必填、默认值、从哪里获取，以及配错时的典型报错。
字段模板见仓库根目录 [`.env.example`](../.env.example)。

## 一、加载机制（先读这一节，能省掉一半的坑）

| 运行方式 | 是否读根目录 `.env` | 说明 |
|---|---|---|
| `docker compose ...` | **是** | Compose 自动加载根目录 `.env` 并做 `${VAR}` 替换 |
| `go run ./cmd/api`（本地） | **否** | API 只读**进程环境变量**，代码里没有 dotenv 解析；必须先导出 |
| `pnpm dev`（前端） | **否** | 前端不读这些变量；API 前缀固定 `/api/v1`，由 Vite 代理转发到 `:8080` |

因此：

- 用 Docker 跑后端 → 只需维护 `.env`。
- 用 `go run` 跑后端 → 必须 `$env:XXX='...'`（PowerShell）或 `export XXX=...`（Bash）逐个导出，`.env` 不会自动生效。

> 另外，`MYSQL_PASSWORD`、`MYSQL_USER`、`MYSQL_ROOT_PASSWORD`、`JWT_SECRET` 在 `docker-compose.yml`
> 里写成了 `${VAR:?...}` 形式：**缺失或为空时 `docker compose` 直接报错退出**，不会回落到弱口令。

---

## 二、数据库

| 变量 | 必填 | 默认值 | 说明 |
|---|---|---|---|
| `MYSQL_HOST` | 否 | `127.0.0.1`（compose 内固定 `mysql`） | 数据库主机 |
| `MYSQL_PORT` | 否 | `3306` | 数据库端口；compose 中该变量只影响**宿主机映射端口** |
| `MYSQL_USER` | **是** | 无（compose 必填） | 业务库账号 |
| `MYSQL_PASSWORD` | **是** | 无 | 业务库密码。代码中已移除 `bike_dev_pwd` 兜底，缺失时连接失败 |
| `MYSQL_DATABASE` | 否 | `bike_enroll` | 业务库名 |
| `MYSQL_ROOT_PASSWORD` | **是**（仅 compose） | 无 | MySQL 容器 root 密码，只在**首次初始化数据卷**时生效 |

**来源**：自建。本地开发任意设置；生产请用强随机串并妥善保管。

**常见错误**

- `Access denied for user 'bike'@'...'`：`MYSQL_PASSWORD` 与容器初始化时不一致。
  容器只在数据卷为空时创建账号，**改密码後必须重建数据卷**才生效：
  ```bash
  docker compose down -v     # ⚠️ 会删除数据库数据
  docker compose up -d mysql
  ```
- `required variable MYSQL_ROOT_PASSWORD is missing a value`：`.env` 未创建或该行留空。
- 从 `127.0.0.1` 改成 `mysql` 却在本机 `go run`：容器内主机名在本机解析不到，本地跑要填 `127.0.0.1`。

---

## 三、Redis

| 变量 | 必填 | 默认值 | 说明 |
|---|---|---|---|
| `REDIS_ADDR` | 否 | `127.0.0.1:6379`（compose 内固定 `redis:6379`） | Redis 地址，注意带 `host:port` |
| `REDIS_PASSWORD` | 否 | 空 | 无密码留空；当前 compose 的 Redis 未开启 `requirepass` |
| `REDIS_DB` | 否 | `0` | 库号 |

**作用**：名额计数器与抢名额 Lua 脚本、报名锁、定时任务互斥锁都依赖 Redis。
Redis 不可用会直接导致报名「锁定名额」失败（不是降级）。

**常见错误**

- `WRONGPASS` / `NOAUTH`：给没设密码的 Redis 配了 `REDIS_PASSWORD`。
- `connection refused`：`REDIS_ADDR` 用了容器名但本机直连。

---

## 四、JWT

| 变量 | 必填 | 默认值 | 说明 |
|---|---|---|---|
| `JWT_SECRET` | **是** | 无 | HS256 签名密钥，**至少 32 字节**；启动时校验 |
| `JWT_EXPIRE_HOURS` | 否 | `168` | Token 有效期（小时） |

**生成方式**

```bash
openssl rand -hex 32      # 64 个十六进制字符 = 32 字节
```

PowerShell 等价写法：

```powershell
-join ((1..32) | ForEach-Object { '{0:x2}' -f (Get-Random -Maximum 256) })
```

**校验规则**（`server/internal/pkg/jwt/manager.go`）

1. 长度 < 32 字节 → `JWT_SECRET must be at least 32 bytes`
2. 命中已知公开默认值（如 `dev-only-change-me-32-bytes-minimum`、`change-me`）→
   `JWT_SECRET must not be a published default; generate one with: openssl rand -hex 32`

**常见错误**

- 服务启动即退出并打印上述两条之一。
- 换过 `JWT_SECRET` 后旧 Token 全部失效，管理后台需重新粘贴 Token。
- 管理后台 Token 存在 `localStorage.bike_admin_token`，必须是 `role=admin` 的 JWT，否则后台接口返回 403。

---

## 五、微信小程序

| 变量 | 必填 | 说明 |
|---|---|---|
| `WECHAT_APPID` | 使用小程序时必填 | 小程序 AppID，形如 `wx` + 16 位十六进制 |
| `WECHAT_SECRET` | 使用小程序时必填 | 小程序 AppSecret（32 位十六进制），**仅后端使用** |

**获取**：登录 [mp.weixin.qq.com](https://mp.weixin.qq.com) → 开发 → 开发管理 → 开发设置 → 开发者 ID。

**用途**：`POST /api/v1/auth/wechat-login` 用小程序传来的 `code` 调 `code2Session` 换取 `openid`，
再签发本系统 JWT。

**常见错误**

- `invalid code` / `40029`：`code` 只能用一次且 5 分钟内有效；重复使用或过期。
- `invalid appid` / `40013`：`WECHAT_APPID` 与产生 `code` 的小程序不一致（例如开发者工具里用的是别人的 AppID）。
- `40125 invalid appsecret`：AppSecret 填错，或已在公众平台重置过。
- **不要把 AppSecret 写进小程序代码**——小程序包可被反编译，泄露后可冒充你的服务端调用微信接口。
  它只能存在于后端环境变量里。

---

## 六、微信支付

### 6.1 先决定：Mock 还是真实支付

| 模式 | 适用对象 | 需要什么 |
|---|---|---|
| `PAYMENT_MODE=mock` | **个人开发者、学习者、没有商户号的情况** | 只需一个自定义的 `MOCK_PAY_SECRET` |
| `PAYMENT_MODE=wechat` | 有微信支付商户号的主体 | 商户号 + API 证书 + APIv3 密钥 + 公网 HTTPS 回调 |

> **关于企业主体**：申请微信支付商户号需要**企业、个体工商户、政府机关、事业单位或社会组织**等主体资质，
> **个人（自然人）无法直接申请**。如果你只是个人开发者、想跑通流程或做课程/毕业设计，
> 请直接使用 `PAYMENT_MODE=mock`——它调用的是与真实回调**同一套**业务逻辑
> （`Service.HandleNotify`），只是不做签名验签，功能验证完全够用。

### 6.2 变量

| 变量 | 必填 | 默认值 | 说明 |
|---|---|---|---|
| `PAYMENT_MODE` | 否 | `wechat` | `mock` / `wechat`；空值按 `wechat` 处理，避免静默降级 |
| `MOCK_PAY_SECRET` | mock 模式必填 | 空 | mock 触发接口的 `X-Mock-Secret` 校验值；**为空时这些接口一律 403** |
| `WECHAT_PAY_MCHID` | wechat 必填 | 空 | 商户号（8~10 位数字） |
| `WECHAT_PAY_SERIAL_NO` | wechat 必填 | 空 | 商户 API 证书序列号（通常 40 位十六进制） |
| `WECHAT_PAY_APIV3_KEY` | wechat 必填 | 空 | APIv3 密钥，**必须正好 32 字节**，回调解密用 |
| `WECHAT_PAY_PRIVATE_KEY_PATH` | wechat 必填 | `./configs/apiclient_key.pem` | 商户 API 私钥路径（容器内为 `/app/configs/...`） |
| `WECHAT_PAY_NOTIFY_URL` | wechat 必填 | 空 | 公网 HTTPS 回调地址，指向 `/api/v1/payments/notify` |
| `WECHAT_PAY_PLATFORM_CERT_PATH` | wechat 必填 | `./configs/wechatpay_platform_cert.pem` | 微信支付平台证书，回调验签用（`.env.example` 未列出，需自行追加） |

**获取位置**

| 值 | 路径 |
|---|---|
| 商户号 `MCHID` | [pay.weixin.qq.com](https://pay.weixin.qq.com) → 账户中心 → 商户信息 |
| 证书序列号 `SERIAL_NO` | 商户平台 → API 安全 → API 证书 |
| APIv3 密钥 | 商户平台 → API 安全 → APIv3 密钥（设置后不可查看，只能重置） |
| 商户私钥 `apiclient_key.pem` | 商户平台 → API 安全 → API 证书 → 证书工具生成 |
| 平台证书 | 商户平台 → API 安全 → 平台证书，或调用 `/v3/certificates` |

证书的放置、权限与轮换见 [../configs/README.md](../configs/README.md) 与
[../server/configs/README.md](../server/configs/README.md)。

**常见错误**

- `wechat pay APIv3 key must be 32 bytes`：密钥不是 32 字节（中文/空格/换行都会被算进长度）。
- `wechat pay private key is not PEM encoded` / `is not RSA` / `parse private key failed`：
  私钥文件损坏、用了平台证书而不是商户私钥、或换行被破坏。
- 回调一直验签失败：`WECHAT_PAY_PLATFORM_CERT_PATH` 指向了**商户证书**（`apiclient_cert.pem`），
  应当填**微信支付平台证书**。
- `APP_ENV=production` 且 `PAYMENT_MODE=mock`：服务拒绝启动（`PAYMENT_MODE=mock is forbidden when APP_ENV="production"`）。
  这是刻意的保护，防止把免签名的 mock 暴露到生产。
- **compose 已知缺口**：`docker-compose.yml` 的 `api.environment` 未透传
  `WECHAT_PAY_APIV3_KEY` 与 `WECHAT_PAY_PLATFORM_CERT_PATH`。用 compose 跑真实支付时需自行补：

  ```yaml
        WECHAT_PAY_APIV3_KEY: ${WECHAT_PAY_APIV3_KEY:-}
        WECHAT_PAY_PLATFORM_CERT_PATH: ${WECHAT_PAY_PLATFORM_CERT_PATH:-./configs/wechatpay_platform_cert.pem}
  ```

  否则即使 `.env` 里填了，容器内也拿不到，回调验签会因缺密钥失败。mock 模式不受影响。

### 6.3 触发 mock 支付/退款

见 README 的「支付模式说明」，或直接看 `server/internal/router/router.go` 的 `registerMockPay`：

```bash
curl -X POST http://localhost:8080/internal/mock-pay/<order_id>/success \
  -H "X-Mock-Secret: <MOCK_PAY_SECRET>"
```

---

## 七、业务参数

| 变量 | 必填 | 默认值 | 说明 |
|---|---|---|---|
| `LOCK_TTL_MINUTES` | 否 | `15` | 名额锁定有效期；超时由定时任务回补名额 |
| `PAY_TIMEOUT_MINUTES` | 否 | `15` | 待支付订单超时关单时间 |
| `LOCK_TTL_SECONDS` | 否 | 空 | **压测专用**：>0 时优先于 `LOCK_TTL_MINUTES`，把锁压到秒级；生产必须留空 |

调小 `LOCK_TTL_MINUTES` 会让「锁过期 → 名额回补」更快可观测，适合联调；
调小会同时增加用户填写表单时锁失效的概率，生产建议保持 10~15 分钟。

---

## 八、服务与容器

| 变量 | 必填 | 默认值 | 说明 |
|---|---|---|---|
| `APP_ENV` | 否 | `development`（compose 默认 `production`） | `production`/`prod` 时启用 Gin Release 并禁止 mock 支付 |
| `API_PORT` | 否 | `8080` | 宿主机映射端口（只影响 compose） |
| `HTTP_ADDR` | 否 | `:8080` | 进程监听地址（只影响 `go run`，compose 内硬编码 `:8080`） |
| `LOG_LEVEL` | 否 | `info` | `debug` / `info` / `warn` / `error` |
| `TZ` | 否 | `Asia/Shanghai` | 时区 |
| `GOPROXY` | 否 | `https://goproxy.io,direct` | 构建阶段 Go 模块代理 |

**常见错误**

- 改了 `API_PORT` 但前端 404：两个前端的 `vite.config.ts` 代理写死了 `http://localhost:8080`，需同步修改。
- 本地 `go run` 想换端口要改 `HTTP_ADDR`，改 `API_PORT` 无效（后者只被 compose 读取）。

---

## 九、代码支持但模板未列出的变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `WECHAT_TPL_PAY_RESULT` | 空 | 支付结果订阅消息模板 ID；为空则跳过推送 |
| `WECHAT_TPL_ENROLL_RESULT` | 空 | 报名结果订阅消息模板 ID；为空则跳过推送 |
| `WECHAT_PAY_PLATFORM_CERT_PATH` | `./configs/wechatpay_platform_cert.pem` | 见 6.2 |

订阅消息未配置不会导致主流程失败，日志会记录 `sender/template not configured` 后跳过。

---

## 十、可直接复制的 `.env` 骨架

仅本地联调、mock 支付的最小可用集合：

```dotenv
MYSQL_ROOT_PASSWORD=change-me-root
MYSQL_USER=bike
MYSQL_PASSWORD=change-me-db
MYSQL_DATABASE=bike_enroll
REDIS_ADDR=127.0.0.1:6379
REDIS_DB=0
JWT_SECRET=<openssl rand -hex 32 的输出>
APP_ENV=development
PAYMENT_MODE=mock
MOCK_PAY_SECRET=<自定义随机串>
WECHAT_APPID=
WECHAT_SECRET=
```

想跑小程序就再补 `WECHAT_APPID` / `WECHAT_SECRET`；想跑真实支付再补 6.2 的 5 个微信支付变量。
