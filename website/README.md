# 骑行赛事官网（website）

Vue 3 + TypeScript + Vite + Tailwind CSS 的赛事官网。

## 功能

- **赛事列表 `/`**
  - 公开赛事列表；
  - 按报名中 / 报名结束 / 已结束筛选；
  - 分页、加载骨架与空状态。
- **赛事详情 `/events/:eventId`**
  - 赛事介绍、规则、时间、地点；
  - 组别、价格、总名额、Redis 实时剩余名额；
  - 「立即报名」跳转到小程序报名指引；
  - 名额已满、组别关闭或赛事非报名中时禁用报名按钮。
- **报名指引 `/enroll-guide`**
  - 展示小程序码；
  - 展示已选赛事/组别；
  - 三步报名说明；
  - 回跳赛事详情。
- 官网只调用公开 GET 接口，不提交报名、不支付、不修改任何数据。

## 小程序码

默认使用 `public/miniapp-code.svg` 演示占位图。部署时配置真实小程序码：

```bash
# .env.local
VITE_MINIPROGRAM_CODE_URL=https://cdn.example.com/miniapp-code.png
```

未配置时，指引页会显示「当前为演示占位码」提示。

## 启动

```bash
cd website
pnpm install
pnpm dev
```

打开 <http://localhost:5174>。联调前启动后端：

```bash
cd server
go run ./cmd/api
```

## API

请求封装在 `src/api/`：

| 文件 | 说明 |
|---|---|
| `http.ts` | fetch、查询串、错误封装 |
| `types.ts` | OpenAPI 对应类型 |
| `events.ts` | 赛事列表、详情 |

- 默认 API 前缀：`/api/v1`；
- Vite 开发代理：`/api` → `http://localhost:8080`；
- 可通过 `VITE_API_BASE` 覆盖 API 前缀。

## 构建

```bash
pnpm build
pnpm preview
```

## 目录

```text
website/
├── public/
│   ├── favicon.svg
│   └── miniapp-code.svg       # 演示占位小程序码
├── src/
│   ├── api/
│   │   ├── http.ts
│   │   ├── types.ts
│   │   └── events.ts
│   ├── components/EventCard.vue
│   ├── router/index.ts
│   ├── styles/index.css        # Tailwind 与公共组件类
│   ├── utils/format.ts
│   ├── views/
│   │   ├── EventListView.vue
│   │   ├── EventDetailView.vue
│   │   ├── EnrollGuideView.vue
│   │   └── NotFoundView.vue
│   ├── App.vue
│   ├── main.ts
│   └── env.d.ts
├── index.html
├── vite.config.ts
├── tsconfig.json
├── package.json
└── pnpm-lock.yaml
```
