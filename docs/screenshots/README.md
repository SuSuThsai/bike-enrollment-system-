# 项目截图

本目录存放 README 与文档引用的截图。官网与管理后台截图由脚本自动生成，小程序截图需人工完成。

## 一、截图清单

| 文件 | 对应页面 | 访问路径 | 当前尺寸 |
|---|---|---|---|
| `website-list.png` | 官网 · 赛事列表 | `http://localhost:5174/` | 842×837（裁剪版） |
| `website-detail.png` | 官网 · 赛事详情 | `http://localhost:5174/events/{event_id}` | 2880×1800 |
| `admin-enrollments.png` | 管理后台 · 报名名单 | `http://localhost:5173/enrollments` | 2880×1800 |
| `miniapp-list.png` | 小程序 · 赛事列表 | 微信开发者工具 → `miniapp/` →「赛事」tab | 380×797 |

> `website-list.png` 目前是手工裁剪的版本（842×837）；重新运行截图脚本会把它覆盖为
> 2880×1800 的完整视口图。`website-detail.png` 与 `admin-enrollments.png` 为脚本生成，
> 规格 1440×900 视口 × `deviceScaleFactor: 2`。

页面路由来源：

- 官网路由：`website/src/router/index.ts` → `/`（列表）、`/events/:eventId`（详情）、`/enroll-guide`
- 后台路由：`admin/src/router/index.ts` → `/events`、`/enrollments`、`/stats`
  （**没有 `/admin` 前缀**，`vite.config.ts` 未设置 `base`）

## 二、重新生成官网 / 后台截图

前置条件：MySQL / Redis / API 已启动，`website` 与 `admin` 的 dev server 已启动，
数据库中有至少一场赛事和若干报名记录。

```powershell
cd scripts
pnpm install            # 首次，安装 playwright-core

# 管理后台需要 JWT：登录后台后，在浏览器控制台执行
#   localStorage.getItem('bike_admin_token')
node screenshot.js --admin-token "<JWT>"
```

可选参数：

| 参数 | 环境变量 | 说明 |
|---|---|---|
| `--website-url` | `WEBSITE_URL` | 官网地址，默认 `http://localhost:5174` |
| `--admin-url` | `ADMIN_URL` | 后台地址，默认 `http://localhost:5173` |
| `--admin-token` | `ADMIN_TOKEN` | 后台 JWT（截图报名名单必填） |
| `--event` | `EVENT_ID` | 详情页赛事；缺省自动从 API 选一场「报名中」的赛事 |
| `--api-base` | `API_BASE` | 默认 `http://localhost:8080/api/v1` |
| `--scroll-y` | `SCROLL_Y` | 截图前向下滚动像素数（默认 0，即页面顶部） |
| `--scroll-to` | `SCROLL_TO` | 截图前滚动到指定 CSS 选择器 |
| `--out` | — | 输出目录，默认 `docs/screenshots` |

脚本通过 `channel: 'chrome'` 驱动本机已安装的 Chrome，无需下载 Chromium；
本机没有 Chrome 时可用 `--channel msedge`，或安装 Playwright 自带 Chromium。

脚本会对页面内容做断言（出现预期文案 + 报名名单表格行数 ≥ 1），
避免路由没匹配却截出一张空白图还当成成功。

## 三、小程序截图（人工）

小程序运行在微信客户端内，无法用 Playwright 自动截图，需在微信开发者工具中人工完成。

### 1. 准备

1. 启动后端与依赖：

   ```powershell
   docker compose up -d mysql redis api
   ```

2. 确认数据库中至少有一场 `lifecycle_status=published`、`registration_status=open` 的赛事，
   否则列表页和详情页会是空状态。

3. 确认 `.env` 中的 `WECHAT_APPID` / `WECHAT_SECRET` 有效——小程序启动时需要
   `wx.login` → `/api/v1/auth/wechat-login` 换取用户身份。

### 2. 加载项目

1. 打开**微信开发者工具** → 「导入项目」。
2. 目录选择仓库中的 `miniapp/`（`project.config.json` 已存在，AppID 为
   `wx4001a36da9a3ec33`；用别人的 AppID 请先改掉）。
3. 详情 → 本地设置 → 勾选**「不校验合法域名、web-view（业务域名）、TLS 版本以及 HTTPS 证书」**。
   `miniapp/config.js` 的 `API_BASE` 是 `http://127.0.0.1:8080/api/v1`，不勾选会请求失败
   （`project.config.json` 中 `urlCheck` 已为 `false`，通常无需再改）。
4. 编译后应进入「赛事」tab。

### 3. 需要截的页面

| 顺序 | 页面 | 进入方式 | 保存文件名 |
|---|---|---|---|
| 1 | 赛事列表 | 首页「赛事」tab（默认页） | `miniapp-list.png`（已有） |
| 2 | 赛事详情 | 在列表点击任一场「报名中」的赛事 | `miniapp-event-detail.png` |
| 3 | 报名表单 | 详情页 → 选择组别 → 「立即报名」 | `miniapp-enroll-form.png` |
| 4 | 支付结果 | 提交报名后进入；或见下方「直接进入」 | `miniapp-payment-result.png` |

### 4. 建议的模拟器尺寸

| 方案 | 机型预设 | 逻辑分辨率 | DPR | 说明 |
|---|---|---|---|---|
| **推荐** | iPhone 12 Pro | 390×844 | 3 | 接近线上主流机型，画质最高 |
| 备选 | iPhone 6/7/8 | 375×667 | 2 | 文件更小，老机型兼容视图 |
| 备选 | iPhone X | 375×812 | 3 | 全面屏基准尺寸 |

在开发者工具顶部机型下拉框中选择。

### 5. 直接进入指定页面（编译模式）

不方便走完整流程时，用工具栏「普通编译」下拉 → **「添加编译模式」**，起始页面填：

| 目标页面 | 编译模式路径 |
|---|---|
| 赛事列表 | `pages/event-list/index` |
| 赛事详情 | `pages/event-detail/index?event_id=<赛事ID>` |
| 报名表单 | `pages/enroll-form/index?event_id=<赛事ID>&group_id=<组别ID>` |
| 支付结果 | `pages/payment-result/index?status=success` |

- `status` 支持 `success` / `pending` / `fail`，对应「支付成功 / 支付未完成 / 支付失败」三态
  （见 `miniapp/pages/payment-result/index.js` 的 `RESULT_META`）。
- 报名表单页缺 `event_id` 或 `group_id` 会直接显示「缺少赛事或组别参数」，**必须**带上两个参数。
- 赛事/组别 ID 可从后台报名名单页或 `GET /api/v1/events` 获取。

### 6. 拍摄与保存

- 用开发者工具模拟器上方的**截图按钮**（或 `Ctrl+Shift+S`）截取模拟器可视区域，
  命名后保存到本目录 `docs/screenshots/`。
- 需要更高画质时，用「预览」二维码在真机打开后手机截图，再传到本目录。
- 建议保留随机真实数据（姓名、组别、金额），不要用全 0 或空白数据，截图更能说明功能。
- 若截图包含真实手机号/身份证等个人信息，请先脱敏或改用测试数据——这些图片会进入版本库。
