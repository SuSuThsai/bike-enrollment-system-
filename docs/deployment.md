# 本地容器编排（MySQL / Redis / Go API）

`docker-compose.yml` 编排三个服务：

| 服务 | 镜像 / 构建 | 端口（宿主机） | 数据 |
|---|---|---|---|
| `mysql` | `mysql:8.0` | `127.0.0.1:3306` | 命名卷 `bike-enroll-mysql-data` |
| `redis` | `redis:7` | `127.0.0.1:6379` | 命名卷 `bike-enroll-redis-data` |
| `api` | `./server/Dockerfile` 构建 | `0.0.0.0:8080` | 只读挂载 `./server/configs` |

Redis 启动参数为 `--appendonly yes --appendfsync everysec`，满足
`docs/constraints.md` 第 5 条对 AOF 的要求。

## 一、当前机器状态（2026-10-24 验证通过）

- Docker Desktop 4.96.3，Docker Engine 29.9.2，Docker Compose v5.6.1
- WSL 2.7.14.0（内核 6.18.33.2-2）
- 三个容器均为 `healthy`
- MySQL 8.0.46；Redis `appendonly=yes`、`appendfsync=everysec`
- Redis 重启后 AOF 中的 key 仍然存在（实测通过）
- Docker 数据磁盘已落到 D 盘：

```text
D:\ultralite\docker\data\disk\docker_data.vhdx
```

Docker Desktop **程序本体**默认装在 `C:\Program Files\Docker\Docker`（安装器没有采用自定义
目录）；真正占空间的镜像、容器、卷都保存在上面这个 D 盘磁盘映像里。

## 二、D 盘布局与目录联接

```text
D:\ultralite\
├── docker\
│   ├── data\
│   │   └── disk\
│   │       └── docker_data.vhdx   # Docker 数据磁盘（镜像/容器/卷）
│   └── desktop\                   # 预留给自定义安装目录（当前未使用）
└── installers\                    # 安装包
```

Docker Desktop 默认从下面的路径读取数据磁盘：

```text
C:\Users\Administration\AppData\Local\Docker\wsl\disk
```

本机把它做成了指向 D 盘的**目录联接（Junction）**：

```powershell
# 已执行，效果如下
C:\Users\Administration\AppData\Local\Docker\wsl\disk
  -> D:\ultralite\docker\data\disk
```

因此 Docker 仍按原路径读写，但 VHDX 实际落在 D 盘。检查方式：

```powershell
Get-Item 'C:\Users\Administration\AppData\Local\Docker\wsl\disk' |
  Select-Object FullName, LinkType, Target
Get-Item 'D:\ultralite\docker\data\disk\docker_data.vhdx' |
  Select-Object FullName, Length
```

> Docker Desktop 升级后有概率重建该目录。升级后如发现 C 盘空间异常增长，先检查上面的
> Junction 是否还在；也可以改用 Docker Desktop 的
> **Settings → Resources → Advanced → Disk image location** 直接指向 D 盘目录。

## 三、镜像加速（国内网络必须）

本机直连 Docker Hub 会失败：

```text
failed to resolve reference "docker.io/library/mysql:8.0": ... EOF
```

已在 `C:\Users\Administration\.docker\daemon.json` 配置镜像加速：

```json
{
  "builder": {
    "gc": {
      "defaultKeepStorage": "20GB",
      "enabled": true
    }
  },
  "experimental": false,
  "registry-mirrors": [
    "https://docker.1panel.live",
    "https://docker.xuanyuan.me",
    "https://docker.m.daocloud.io"
  ]
}
```

改完后重启 Docker Desktop，确认配置生效：

```powershell
docker desktop restart
docker info | Select-String 'Registry Mirrors' -Context 0,4
docker pull alpine:3.22
```

镜像站可能随时失效；如果拉取仍然失败，替换 `registry-mirrors` 中可用的地址，或配置
公司/本地 HTTP 代理。`docker compose` 拉取 `mysql`、`redis`、`golang`、`alpine` 都会走这里。

## 四、启动编排

```powershell
cd D:\ridingCode+wechat+webside
Copy-Item .env.example .env   # 首次执行；已有 .env 会覆盖，注意备份
# 按需修改 .env 中的密码、JWT_SECRET、微信参数
docker compose config
docker compose up -d --build
docker compose ps
```

首次启动通过镜像站拉取 MySQL、Redis、Go 构建镜像，并构建 `bike-enroll/api:local`。构建阶段
下载 Go 模块默认走 `GOPROXY=https://goproxy.io,direct`，可在 `.env` 中覆盖后重新执行
`docker compose build api`。

当前实测结果：

```text
NAME                IMAGE                   STATUS
bike-enroll-api     bike-enroll/api:local   Up (healthy)   0.0.0.0:8080->8080/tcp
bike-enroll-mysql   mysql:8.0               Up (healthy)   127.0.0.1:3306->3306/tcp
bike-enroll-redis   redis:7                 Up (healthy)   127.0.0.1:6379->6379/tcp
```

## 五、验证

### API

```powershell
curl.exe -i http://localhost:8080/healthz
# HTTP/1.1 200 OK
# {"status":"ok"}

curl.exe -i http://localhost:8080/api/v1/events
# HTTP/1.1 200 OK
# {"items":[...],"total":N}
```

管理后台 Vite 代理也已验证：`http://localhost:5173/api/v1/events` 能透传到该 API 容器。

### Redis AOF

```powershell
docker compose exec redis redis-cli CONFIG GET appendonly
# appendonly
# yes

docker compose exec redis redis-cli CONFIG GET appendfsync
# appendfsync
# everysec

docker compose exec redis redis-cli INFO persistence | Select-String 'aof_enabled'
# aof_enabled:1
```

AOF 文件位于容器 `/data/appendonlydir/`，对应命名卷 `bike-enroll-redis-data`。

持久化实测：

```powershell
docker compose exec redis redis-cli SET bike:smoke aof-ok
docker compose restart redis
docker compose exec redis redis-cli GET bike:smoke
# "aof-ok"        ← 重启后仍在，说明 AOF 生效
docker compose exec redis redis-cli DEL bike:smoke
```

### MySQL

```powershell
echo 'SELECT VERSION();' | docker compose exec -T mysql sh -c 'mysql -u"$MYSQL_USER" -p"$MYSQL_PASSWORD"'
# 8.0.46
```

数据库 `bike_enroll` 与用户 `bike` 由镜像首次启动时根据 `.env` 自动创建。修改
`MYSQL_DATABASE` / `MYSQL_USER` 后，仅对空数据卷生效；已有数据卷需手动处理。

## 六、常用命令

```powershell
# 只看日志
docker compose logs -f api

# 改代码后重建并重启 API
docker compose up -d --build api

# 只启动依赖，后端在宿主机 go run（端口 8080 不会被占用）
docker compose up -d mysql redis
cd server
go run ./cmd/api

# 停止但保留数据
docker compose down

# 停止并删除数据卷（会丢失数据库数据，慎用）
docker compose down -v

# 进入容器
docker compose exec mysql bash
docker compose exec redis sh
```

## 七、端口与数据说明

- MySQL / Redis 端口只绑定 `127.0.0.1`，不会暴露到局域网；端口冲突时可在 `.env`
  中改 `MYSQL_PORT` / `REDIS_PORT`。
- API 暴露 `8080`，官网/管理后台的开发代理都指向它。
- 数据卷为 `bike-enroll-mysql-data`、`bike-enroll-redis-data`；`docker compose down`
  不会删除它们，实际文件都在 D 盘的 `docker_data.vhdx` 中。
- 微信支付私钥放在 `server/configs/apiclient_key.pem`，以只读方式挂载进容器，不会打进镜像
  （`server/.dockerignore` 已排除 `configs/*.pem`）。
- `.env` 已被根目录 `.gitignore` 忽略，不要提交真实密码与密钥。

## 八、故障排查

| 现象 | 处理 |
|---|---|
| `Docker Desktop is unable to start` | `wsl --version` 必须 ≥ 2.1.5；本机使用 2.7.14。旧版 inbox WSL 需安装 `wsl.2.7.14.0.x64.msi` 或从 Microsoft Store 安装 |
| 拉镜像 `EOF` / `failed to resolve reference` | Docker Hub 不可达，检查 `registry-mirrors` 并 `docker desktop restart` |
| 构建时 Go 模块下载失败 | 调整 `.env` 的 `GOPROXY`，`docker compose build api` |
| 端口被占用 | 修改 `.env` 中 `API_PORT` / `MYSQL_PORT` / `REDIS_PORT`，同步调整管理后台代理 |
| C 盘空间异常增长 | 检查 `wsl\disk` Junction 是否丢失，或把 Disk image location 改到 D 盘 |

## 九、生产前必须调整

- 设置 `APP_ENV=production` 与 `PAYMENT_MODE=wechat`。`APP_ENV=production/prod` 且
  `PAYMENT_MODE=mock` 时 API 会拒绝启动。
- 替换 `.env` 中所有默认密码与 `JWT_SECRET`（32 字节以上随机串）。
- 不要把 `3306` / `6379` 暴露到公网；如确需远程访问，使用 SSH 隧道或内网。
- API 前置反向代理与 TLS；`WECHAT_PAY_NOTIFY_URL` 使用公网 HTTPS 地址。
- 为 MySQL 数据卷配置备份；Redis AOF 文件同样需要纳入备份/恢复演练。
