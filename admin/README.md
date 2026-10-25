# 骑行赛事管理后台（admin）

Vue 3 + TypeScript + Vite + Element Plus 管理后台，已实现赛事、组别、报名名单与统计页面。

## 功能

- **赛事管理**：公开赛事列表、状态筛选、创建赛事、编辑赛事、删除赛事（仅当赛事下没有组别）。
- **组别管理**：在赛事详情下查看组别、创建组别、编辑组别、删除组别（仅当组别无锁/报名/订单）。
  - 修改 `total_slots` 时由后端原子同步 Redis；
  - 新剩余为负时后端返回 `GROUP_SOLD_OUT`；
  - **重建名额**：按对账等式补建/覆盖 Redis `group:{id}:remaining`，计数已存在时可二次确认强制覆盖；
  - **释放健康检查**：只读诊断每组「过期 >1 分钟仍 active 的锁」与「pending_payment 超过 15 分钟未释放的订单」，
    可展开查看 token / 订单样例。
- **报名名单**：按赛事、组别、报名状态筛选，分页查看，导出 Excel。
  - 导出上限 10000 条，由后端截断；
  - 列表中的敏感表单字段由后端脱敏。
- **统计看板**：按赛事展示各组总名额、Redis 剩余、活跃锁、待支付、已支付/确认数量，
  并校验对账等式。

## 鉴权

admin 接口要求 `Authorization: Bearer <JWT>` 且 `role=admin`。当前后端没有管理员登录接口，
因此页面右上角提供「设置 Token」：

1. 使用测试环境签发的管理员 JWT；
2. 粘贴到对话框并保存；
3. Token 保存在浏览器 `localStorage` 的 `bike_admin_token`。

未配置或 Token 无效时，接口会返回 401/403，页面以错误提示展示。

## 启动

```bash
cd admin
pnpm install
pnpm dev
```

打开 <http://localhost:5173>。代理目标为 `localhost:8080`，联调前先启动后端：

```bash
cd server
go run ./cmd/api
```

## API 代理

`vite.config.ts` 中的开发代理：

| 前端路径 | 目标 | 配置 |
|---|---|---|
| `/api` | `http://localhost:8080` | `changeOrigin: true`，不做 rewrite |

前端请求 `/api/v1/admin/...` 会透传到
`http://localhost:8080/api/v1/admin/...`。

## 脚本

```bash
pnpm dev        # 开发服务器 :5173
pnpm build      # vue-tsc --noEmit && vite build
pnpm preview    # 预览构建产物
pnpm typecheck  # 仅类型检查
```

## 目录结构

```text
admin/
├── public/favicon.svg
├── src/
│   ├── api/
│   │   ├── http.ts              # fetch 封装、Bearer Token、错误与文件下载
│   │   ├── types.ts             # OpenAPI 对应类型
│   │   ├── events.ts            # 赛事列表/详情/创建/更新
│   │   ├── groups.ts            # 组别创建/更新
│   │   ├── enrollments.ts       # 报名名单/导出
│   │   └── stats.ts             # 统计看板
│   ├── router/index.ts          # 赛事/组别/名单/统计路由
│   ├── stores/
│   │   ├── index.ts             # Pinia 实例
│   │   └── auth.ts              # 管理员 Token
│   ├── styles/index.css         # 全局布局与页面样式
│   ├── utils/format.ts          # 日期、金额、表单数据格式化
│   ├── views/
│   │   ├── EventListView.vue    # 赛事列表 + 创建/编辑
│   │   ├── EventDetailView.vue  # 赛事详情 + 组别管理
│   │   ├── EnrollmentListView.vue
│   │   └── StatsOverviewView.vue
│   ├── App.vue                  # 外壳、菜单、Token 设置
│   ├── main.ts                  # Element Plus / Pinia / Router
│   └── env.d.ts
├── index.html
├── vite.config.ts
├── tsconfig.json
├── package.json
└── pnpm-lock.yaml
```

## 字段约定

- 金额字段 `price`、`min_price` 单位为**分**，页面按元展示。
- 时间字段使用 ISO 8601（RFC3339）提交。
- 组别 `form_schema` 使用 JSON 文本编辑，保存前校验必须为 JSON 对象。
- 赛事列表使用公开接口 `GET /events`（OpenAPI 没有单独的 admin 列表接口）。
- 组别详情使用公开接口 `GET /events/{event_id}` 返回的 `groups`。

## 构建

Element Plus 采用全量引入；当前构建产物有 >500 kB chunk 提示，后续可按需引入优化。
