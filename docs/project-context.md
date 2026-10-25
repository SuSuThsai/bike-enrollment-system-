# 项目结构与技术栈说明

## 一、技术栈（已锁定，不得变更）

| 层 | 选型 | 版本要求 |
|---|---|---|
| 后端框架 | Gin | v1.10+ |
| 主存储 | MySQL | 8.0 |
| 缓存与并发控制 | Redis | 7.x，必须开启 AOF |
| ORM | GORM | v2 |
| 官网前端 | Vue 3 + Vite + Pinia + Tailwind CSS | Vue 3.4+ |
| 管理后台 | Vue 3 + Vite + Element Plus + Pinia | Element Plus 2.8+ |
| 小程序 | 原生微信小程序 | 基础库 3.0+ |
| 部署 | Docker Compose | 本地与单机生产共用 |

## 二、目录结构

bike-enroll/
├── docs/
│ ├── openapi.yaml
│ ├── state-machine.md
│ ├── project-context.md
│ ├── constraints.md
│ └── design-verify/ # 穷举脚本 + 审计报告 + 契约测试骨架快照
├── server/ # Go 后端
│ ├── cmd/api/main.go # 入口
│ ├── internal/
│ │ ├── handler/ # HTTP handler，按 tag 分子目录
│ │ │ ├── auth/
│ │ │ ├── event/
│ │ │ ├── enrollment/
│ │ │ ├── payment/
│ │ │ └── admin/
│ │ ├── service/ # 业务逻辑
│ │ ├── repository/ # 数据访问（MySQL + Redis）
│ │ ├── model/ # GORM 模型
│ │ ├── middleware/ # JWT、日志、限流
│ │ ├── job/ # 定时任务（lock 过期、支付超时、对账）
│ │ ├── contract/ # 基于 openapi.yaml 的契约测试骨架
│ │ └── pkg/ # 工具包（lua 脚本、payment provider、wechat、excel）
│ ├── migrations/ # golang-migrate SQL
│ ├── configs/
│ └── go.mod
├── admin/ # Vue 管理后台
│ ├── src/
│ │ ├── api/ # 按 openapi 生成或手写
│ │ ├── views/
│ │ ├── components/
│ │ ├── stores/
│ │ └── router/
│ └── vite.config.ts
├── website/ # Vue 官网
│ ├── src/
│ │ ├── api/
│ │ ├── views/ # 列表、详情、跳转引导
│ │ └── components/
│ └── vite.config.ts
├── miniapp/ # 原生微信小程序
│ ├── app.js
│ ├── app.json
│ ├── utils/
│ │ ├── request.js # 统一注入 token、错误码处理
│ │ └── storage.js # 表单本地暂存
│ └── pages/
│ ├── event-list/
│ ├── event-detail/
│ ├── enroll-form/
│ ├── payment-result/
│ └── my-enrollments/
├── docker-compose.yml
└── .env.example


## 三、各端职责边界

| 端 | 职责 | 禁止事项 |
|---|---|---|
| 小程序 | 报名、支付、订阅消息授权 | 不直接展示赛事富文本详情（跳官网或简化展示） |
| 官网 | 赛事列表、详情、跳转小程序 | 不承载任何写操作；不直接提交报名 |
| 管理后台 | 赛事/组别配置、名单查询、导出、统计 | 不直接修改 Redis 计数器，必须通过 service 层封装的接口 |
| Go 后端 | 唯一数据写入口；名额计数器唯一操作方 | 不允许任何端绕过 service 层直接读写 Redis |

## 四、环境变量（.env.example）

MySQL
MYSQL_HOST=127.0.0.1
MYSQL_PORT=3306
MYSQL_USER=bike
# 自行设置，无默认值
MYSQL_PASSWORD=
MYSQL_DATABASE=bike_enroll

Redis
REDIS_ADDR=127.0.0.1:6379
REDIS_PASSWORD=
REDIS_DB=0

JWT
JWT_SECRET=<32 字节随机串>
JWT_EXPIRE_HOURS=168

微信小程序
WECHAT_APPID=
WECHAT_SECRET=

微信支付
WECHAT_PAY_MCHID=
WECHAT_PAY_SERIAL_NO=
WECHAT_PAY_PRIVATE_KEY_PATH=./configs/apiclient_key.pem
WECHAT_PAY_NOTIFY_URL=https://api.example.com/api/v1/payments/notify
WECHAT_PAY_API_V3_KEY=
WECHAT_PAY_PLATFORM_CERT_PATH=./configs/wechatpay_platform_cert.pem

支付提供者
# mock（本地联调，不验签）：/internal/mock-pay/* 路由可用；
# wechat（真实微信支付）：/internal/mock-pay/* 路由不注册（404）。
# APP_ENV=production/prod 且 PAYMENT_MODE=mock 时服务拒绝启动。
# 空值按 wechat 处理，不会静默降级为 mock。
PAYMENT_MODE=wechat
# 仅 mock 模式下 /internal/mock-pay/* 的 X-Mock-Secret 校验值；wechat 模式下无作用；
# 为空时 mock 接口一律 403。
MOCK_PAY_SECRET=

订阅消息模板
WECHAT_TPL_PAY_RESULT=
WECHAT_TPL_ENROLL_RESULT=

业务参数
LOCK_TTL_MINUTES=15
PAY_TIMEOUT_MINUTES=15


## 五、本地启动

docker compose up -d mysql redis # 仅启动 MySQL + Redis；全栈用 docker compose up -d --build
cd server && go run ./cmd/api # 后端 :8080
cd admin && pnpm dev # 管理后台 :5173
cd website && npm run dev # 官网 :5174

小程序用微信开发者工具打开 miniapp/，勾选“不校验合法域名”


## 六、命名与代码约定

- 所有金额字段单位为**分**，类型 `int`，字段名以 `amount` / `price` 结尾；
- 所有时间字段后端返回 **RFC3339 字符串**，前端不做时区转换；
- 所有 ID 使用 **UUID v4** 字符串，不用自增主键对外暴露；
- 错误响应统一 `{ code, message, request_id }`，`code` 取 openapi.yaml 中枚举值；
- Go 代码禁止在 handler 层直接操作 Redis，所有 Redis 操作封装在 `repository/` 下。