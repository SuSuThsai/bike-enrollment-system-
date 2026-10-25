# configs/

> **本目录不是证书存放位置。** 请勿在此放置 `*.pem` / `*.key` / `*.p12` / `*.pfx`。

微信支付证书实际放在 **[`server/configs/`](../server/configs/README.md)**，原因如下：

1. `docker-compose.yml` 只把宿主机的 `./server/configs` 挂载进容器：

   ```yaml
   volumes:
     - ./server/configs:/app/configs:ro
   ```

2. API 进程的工作目录是容器内 `/app`，因此 `.env` 里的相对路径：

   ```dotenv
   WECHAT_PAY_PRIVATE_KEY_PATH=./configs/apiclient_key.pem
   WECHAT_PAY_PLATFORM_CERT_PATH=./configs/wechatpay_platform_cert.pem
   ```

   解析为容器内的 `/app/configs/...`，对应宿主机的 **`server/configs/...`**。

如果把证书放在仓库根目录的 `configs/`，容器内 `/app/configs` 里仍然是空的，服务会启动失败或回调验签失败。

## 正确的目录结构

```text
server/configs/
├── README.md                          # 入库：证书获取与放置说明
├── apiclient_key.pem                  # 不入库：商户 API 私钥
└── wechatpay_platform_cert.pem        # 不入库：微信支付平台证书
```

完整的获取步骤、权限要求（`chmod 600`）与轮换流程见
**[server/configs/README.md](../server/configs/README.md)**；
各环境变量的含义与常见报错见 **[docs/CONFIGURATION.md](../docs/CONFIGURATION.md)**。

## 为什么这个目录存在

部分部署文档与框架约定使用根目录 `configs/`，为避免有人照着旧路径把证书放错位置，
这里保留一份指向说明。若你不需要，可以直接删除本目录与文件，不影响构建和运行。
