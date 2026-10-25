# 骑行赛事报名小程序（miniapp）

原生微信小程序，覆盖赛事浏览、组别选择、动态报名表单、支付结果与我的报名。

## 页面

| 页面 | 路径 | 说明 |
|---|---|---|
| 赛事列表 | `pages/event-list/index` | 状态筛选、下拉刷新、触底加载 |
| 赛事详情 | `pages/event-detail/index` | 赛事信息、组别选择、实时剩余名额 |
| 报名表单 | `pages/enroll-form/index` | 按 `form_schema` 动态渲染、本地草稿、名额锁、提交与支付 |
| 支付结果 | `pages/payment-result/index` | 成功/待支付/失败状态、继续支付 |
| 我的报名 | `pages/my-enrollments/index` | 报名列表、待支付订单继续支付 |

## 关键实现

### 动态表单

`utils/schema.js` 支持：

- `{ fields: [...] }` 或直接字段数组；
- `text`、`textarea`、`number`、`phone`、`id_card`、`date`、`select`、`radio`、`checkbox`；
- required、placeholder、options、maxlength、default；
- `phone`、`id_card` 基础格式校验；
- 提交时组装 `form_data`。

示例：

```json
{
  "fields": [
    { "name": "name", "label": "姓名", "type": "text", "required": true },
    { "name": "phone", "label": "手机号", "type": "phone", "required": true },
    { "name": "size", "label": "服装尺码", "type": "select", "options": ["S", "M", "L"] }
  ]
}
```

### 本地暂存与网络补传

- 输入过程中按组别保存草稿到 `wx.setStorageSync`；
- 重新进入表单自动恢复草稿；
- 提交时网络失败，将完整报名 payload 写入本地待补传队列；
- App `onShow`、`onNetworkStatusChange` 网络恢复时自动补传；
- 补传成功提示到「我的报名」查看；
- `ENROLLMENT_DUPLICATE` 视为已提交成功；`LOCK_EXPIRED` 等不可重试错误会丢弃待补传项并记录日志。

### 订阅消息

- `utils/subscribe.js` 在支付前调用 `wx.requestSubscribeMessage`；
- 表单页在用户点击「提交并支付」时拉起；
- 我的报名「继续支付」通过 `payOrder` 统一拉起；
- 用户接受后调用 `POST /notifications/subscribe` 上报模板 ID；
- 授权或上报失败不阻断报名/支付。

模板 ID 在 `config.js` 的 `SUBSCRIBE_TEMPLATE_IDS` 中配置；为空时跳过授权并输出 warning。

### 可访问性

- 关键按钮、筛选项、表单选项 `min-height: 88rpx`（约 44pt）；
- 正文深色 `#0f172a` 配浅底，关键操作使用深绿 `#047857` / 白字；
- 状态标签使用深色文字 + 浅色背景，避免户外强光下低对比度；
- 固定底栏预留 `env(safe-area-inset-bottom)`。

## 启动

1. 启动后端：

```bash
cd server
go run ./cmd/api
```

2. 用微信开发者工具导入 `miniapp/`；
3. 在「详情 → 本地设置」勾选“不校验合法域名、web-view（业务域名）、TLS 版本以及 HTTPS 证书”；
4. 修改 `config.js`：

```js
API_BASE: 'http://127.0.0.1:8080/api/v1'
```

5. 填写真实小程序 AppID（`project.config.json`）与订阅模板 ID。

## 登录与请求

- `utils/auth.js` 调用 `wx.login` → `POST /auth/wechat-login` → 保存 JWT；
- `utils/request.js` 统一注入 `Authorization: Bearer <token>`；
- 统一处理业务错误码、401 清理 token、request_id 日志与网络错误标记。

## 依赖的后端接口

- `GET /events`
- `GET /events/:event_id`
- `POST /auth/wechat-login`
- `POST /enrollments/lock`
- `POST /enrollments`
- `GET /enrollments/my`
- `POST /payments/prepay`
- `POST /notifications/subscribe`

说明：`GET /enrollments/my` 已接入后端列表接口（`form_data` 脱敏返回）；页面展示服务端真实报名记录，
不会伪造数据。`GET /enrollments/{enrollment_id}` 仅报名本人可读。
