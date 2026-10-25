# server/configs — 证书与密钥放置说明

本目录**只存放运行时挂载的证书与私钥文件**，不存放任何明文凭据。

## 目录约定

- 版本库中**只保留本 README**（`.gitkeep` 亦可）。
- 真实的 `*.pem` / `*.key` / `*.p12` / `*.pfx` 由**使用者自行放入**，永不提交。
- 根目录 `.gitignore` 已忽略 `*.pem`、`*.key`、`*.p12`、`*.pfx`、`configs/*.pem`、`configs/*.key`；
  若新增其他后缀，请同步补充忽略规则。

## 需要哪些文件

| 文件名（默认） | 用途 | 是否必需 |
|---|---|---|
| `apiclient_key.pem` | 商户 API 私钥，请求微信支付接口时签名 | `PAYMENT_MODE=wechat` 时必需 |
| `wechatpay_platform_cert.pem` | 微信支付平台证书，回调解密与验签 | `PAYMENT_MODE=wechat` 时必需 |

文件名由环境变量决定，可自定义：

- `WECHAT_PAY_PRIVATE_KEY_PATH`（默认 `./configs/apiclient_key.pem`）
- `WECHAT_PAY_PLATFORM_CERT_PATH`（默认 `./configs/wechatpay_platform_cert.pem`）

`PAYMENT_MODE=mock` 本地联调时两个文件都不需要。

## 从哪里获取

1. **商户 API 私钥 `apiclient_key.pem`**
   登录 <https://pay.weixin.qq.com> → 账户中心 → **API 安全** → API 证书 →
   申请证书后用「证书工具」生成，得到 `apiclient_key.pem`（私钥）与 `apiclient_cert.pem`（证书）。
   **私钥只在申请时下发一次**，请立即归档，丢失只能重新申请。

2. **商户号 / 证书序列号**
   商户平台 → 账户中心 → 商户信息，取得 `mchid`（写入 `WECHAT_PAY_MCHID`）；
   商户平台 → API 安全 → API 证书，查看证书序列号（写入 `WECHAT_PAY_SERIAL_NO`）。

3. **APIv3 密钥**
   商户平台 → API 安全 → APIv3 密钥，设置 32 字节字符串（写入 `WECHAT_PAY_APIV3_KEY`）。
   该密钥用于回调解密，**不要**与 API 密钥混用。

4. **微信支付平台证书 `wechatpay_platform_cert.pem`**
   商户平台 → API 安全 → 平台证书，或调用 `/v3/certificates` 接口下载。

## 放置位置与权限

```text
server/configs/
├── README.md                         # 本文件（入库）
├── apiclient_key.pem                 # 不入库
└── wechatpay_platform_cert.pem       # 不入库
```

**权限要求**（Linux / 容器宿主）：

```bash
chmod 700 configs
chmod 600 configs/apiclient_key.pem
chmod 644 configs/wechatpay_platform_cert.pem   # 平台证书为公开信息
```

- 私钥属主应为运行服务的账号，**禁止** `777` 或 world-readable。
- 容器内以只读方式挂载：`docker-compose.yml` 中 `./server/configs:/app/configs:ro`，
  进程无法回写，降低被篡改风险。
- 私钥**不要**打进镜像：`server/.dockerignore` 已排除 `configs/*.pem`、`configs/*.key`。

## 轮换与泄露处置

- 怀疑泄露时：立即在商户平台重新申请 API 证书（旧证书作废），更新本目录文件并重启服务。
- 任何情况下都不要把私钥内容粘贴进 `.env`、聊天记录、日志或工单；
  `.env` 只保存**路径**，不保存私钥本体。
