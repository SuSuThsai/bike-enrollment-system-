# 抢名额压测（lock-oversell）

验证「Redis Lua 原子扣减」在高并发下不超卖：200 个 VU、每个 VU 连发 10 次
`POST /api/v1/enrollments/lock`，共 2000 次请求，全部使用互不相同的 `user_id`
（脚本用 `JWT_SECRET` 本地签发压测 JWT，不走登录接口）。

## 文件

| 文件 | 作用 |
|---|---|
| `lock-oversell.js` | k6 脚本：发压、按状态码计数、把汇总写入 `results/lock-oversell-summary.json` |
| `run-lock-oversell.ps1` | 运行器：预检 → 调 k6 → 对账（Redis / MySQL）→ 断言，可选复位 |

## 前置条件

1. `docker compose up -d` 三件套健康，API 在 `http://127.0.0.1:8080`；
2. 仓库根目录存在 `.env`（提供 `JWT_SECRET` 与 MySQL 连接信息）；
3. 压测组别处于干净状态：`Redis group:{id}:remaining = groups.total_slots`，且
   `enrollment_locks` 中没有该组别的 `active` 记录（脚本会预检并 halt）。

压测环境初始化参考上一轮：创建 `total_slots=100, price=1` 的组别并确认
`group:{group_id}:remaining=100`。若组别计数缺失，可用
`POST /admin/groups/{group_id}/rebuild-remaining` 修复。

## 运行

```powershell
# 本机已安装 k6（https://k6.io/docs/get-started/installation/）
.\tests\load\run-lock-oversell.ps1

# 未安装 k6：用 grafana/k6 容器跑（先拉镜像：docker pull grafana/k6:latest）
.\tests\load\run-lock-oversell.ps1 -UseDocker

# 指定组别 / 并发 / 每个 VU 的次数
.\tests\load\run-lock-oversell.ps1 -GroupId <uuid> -Vus 200 -IterationsPerVu 10

# 压测后立刻复位（删除压测 lock 记录 + 调 rebuild-remaining?force=true）
.\tests\load\run-lock-oversell.ps1 -ResetAfter
```

> Windows 若提示「在此系统上禁止运行脚本」，用
> `powershell -NoProfile -ExecutionPolicy Bypass -File tests\load\run-lock-oversell.ps1`。

参数一览：`-GroupId`、`-BaseUrl`、`-Vus`、`-IterationsPerVu`、`-K6Path`、
`-UseDocker`、`-DockerNetwork`、`-DockerBaseUrl`、`-ResetAfter`、`-SkipPrecheck`。

## 输出

```
=== 压测结果 ===
请求计划 / 实际      : 2000 / 2000
HTTP 200 成功锁定    : 100
HTTP 409 名额已满    : 1900
其它状态码           : 0
k6 运行时长          : ~1000 ms
lock 延迟(ms)        : avg=... p95=... p99=... max=...

=== 对账结果 ===
1) 成功锁定数（HTTP 200）           : 100
2) Redis group:{id}:remaining       : 0
3) MySQL active lock 记录数         : 100
   groups.total_slots               : 100

=== 断言 ===
[PASS] 成功锁定数 100 <= total_slots 100
[PASS] Redis remaining 0 + active locks 100 = 100 = total_slots
[PASS] 状态码合计 2000 = 计划请求数 2000
```

退出码：`0` 全部通过；`1` 任一条断言失败（超卖、对账不平衡、计数 key 缺失等）。

## 注意

- 每次成功锁定会占用名额，锁 TTL 为 15 分钟：跑完不加 `-ResetAfter` 时，
  组别会处于「已售罄」状态，等超时任务回补或手动复位；
- `-ResetAfter` 的复位动作：删除该组别的 `enrollment_locks` 记录 → 清空 Redis 中
  **所有** `lock:*` 辅助 key（`lock:{token}` / `lock:{user}:{group}` /
  `lock:rollback:{token}`，权威状态在 MySQL）→ 调
  `POST /admin/groups/{id}/rebuild-remaining?force=true` 把剩余名额按对账等式重建回 `total_slots`；
  因此它是给**专用压测环境**用的，共享环境请谨慎；
- k6 的 `thresholds` 已内置 `lock_http_200 count <= total_slots`，
  超卖时 k6 自身退出码为 99，脚本仍会继续做对账并给出两项断言结果；
- 409（`GROUP_SOLD_OUT`）是预期结果，已通过 `http.expectedStatuses(200, 409)`
  声明，不会计入 `http_req_failed`。

---

# 支付回调幂等压测（notify-idempotent）

验证「重复支付回调」被正确幂等处理：先创建 10 笔 `pending_payment` 订单
（每笔对应一个已消费的 lock），再对每笔订单并发发起 10 次
`POST /internal/mock-pay/{order_id}/success`（模拟微信重复回调），共 100 次。

期望：100 次回调全部返回 200；每单只有一次 `pending_payment → paid` 迁移，
其余 9 次命中「重复回调直接 COMMIT」分支；Redis `remaining` 不被回调改动。

## 文件

| 文件 | 作用 |
|---|---|
| `notify-idempotent.js` | k6 脚本：`setup()` 走报名链路创建 10 笔订单，主流程发 100 次重复回调 |
| `run-notify-idempotent.ps1` | 运行器：预置用户 → 预检 → 调 k6 → 对账（MySQL/Redis/API 日志）→ 断言，可选复位 |

## 前置条件

1. 同上一节：compose 三件套健康、`.env` 可用、`PAYMENT_MODE=mock` 且 `MOCK_PAY_SECRET` 非空；
2. 组别处于干净状态（无 lock/报名/订单/协议签署，`remaining = total_slots`），运行器会预检；
3. 订单通过真实报名接口创建，`users` 表需要有对应用户：运行器会先插入
   `ORDERS` 个压测用户（`openid` 前缀 `wx-<runTag>-`），`-ResetAfter` 时一并删除。

## 运行

```powershell
# 容器模式（推荐）
powershell -NoProfile -ExecutionPolicy Bypass -File tests\load\run-notify-idempotent.ps1 -UseDocker

# 跑完自动复位（删订单/报名/锁/协议签署/压测用户，重建 remaining）
powershell -NoProfile -ExecutionPolicy Bypass -File tests\load\run-notify-idempotent.ps1 -UseDocker -ResetAfter
```

参数一览：`-GroupId`、`-BaseUrl`、`-EventId`、`-DisclaimerVersion`、`-Orders`、
`-Duplicates`、`-K6Path`、`-UseDocker`、`-DockerNetwork`、`-DockerBaseUrl`、
`-ResetAfter`、`-SkipPrecheck`。

## 输出

```
=== 逐单状态 ===
order(前12)     order  paid_at              updated=paid   enroll   applied dup    txuniq
15311039aa7f   paid   2026-10-25 16:18:47  yes            paid     1      9      10
...

=== 对账结果 ===
1) Order.status='paid' 数量        : 10 / 10
2) Enrollment.status='paid' 数量   : 10 / 10
   paid_at 已设置 / updated_at=paid_at : 10 / 10
3) Redis remaining 回调前 / 回调后  : 90 / 90

=== 断言 ===
[PASS] 10 笔订单全部为 paid
[PASS] 10 笔 Enrollment 全部为 paid
[PASS] paid_at 均已设置，且 updated_at = paid_at（只发生一次状态迁移）
[PASS] Redis remaining 回调前后一致：90
[PASS] 100 次回调全部返回 200（重复回调同样 200）
[PASS] 日志取证：每单 applied=1、duplicate=9
[PASS] 每单收到 10 次 mock 回调请求（日志 verified）
[PASS] 每单的 10 个 TransactionID 互不相同（同一订单收到不同事务号）
[PASS] 落库 transaction_id 属于该单回调集合（只写入一次，未被后续回调覆盖）
```

## 通过标准

1. 所有订单最终 `status='paid'`，`paid_at` 只设置一次（`updated_at = paid_at`）；
2. Redis `remaining` 与回调前**完全一致**——回调路径不得扣减/回补名额。
   若 remaining 减少，说明支付回调错误地执行了名额扣减，违反
   `docs/constraints.md` 第 11 条（支付回调不扣减 Redis）。

## 说明

- 「paid_at 只被设置一次」从三处取证：`paid_at` 非空、`updated_at = paid_at`
  （重复回调不执行 UPDATE，因此不会被推后）、API 日志中每单
  `payment notify applied=1` 且 `duplicate payment notify ignored=9`；
- **TransactionID 取证**：mock 模式下 `/internal/mock-pay/:order_id/success`
  每次回调都会随机生成 `TransactionID`。运行器从 API 日志按订单收集事务号，
  要求每单 10 个**互不相同**，并断言落库 `orders.transaction_id` 属于该集合。
  这样才能区分两种实现：
  - 幂等键是 `Order.status` → 10 个不同事务号仍只迁移一次（本脚本 PASS）；
  - 幂等键是 `TransactionID` → 10 个事务号被当成 10 个事件，会出现重复迁移、
    `applied>1`、`updated_at` 被推后，本脚本直接 FAIL——这正是该压测要抓的缺陷；
- 回调前后 `remaining` 由 k6 `setup()` 通过公开详情接口（Redis 口径）记录，
  写入 `results/notify-idempotent-summary.json` 的 `redis_remaining_before`；
- `-ResetAfter` 会清理该组别的 `disclaimer_signatures`/`orders`/`enrollments`/
  `enrollment_locks`、全部 `lock:*` 辅助 key、本次预置的压测用户，并用
  `rebuild-remaining?force=true` 把名额复位。

---

# Go 竞态测试（race_release_vs_notify）

「释放 vs 回调」竞态用例位于 **server 模块内**：

```text
server/tests/load/race_release_vs_notify_test.go
```

> 为什么不放在仓库根的 `tests/load/`：该用例要直接调用
> `job.PayTimeoutJob` / `payment.Service.HandleNotify` 等
> `bike-enroll/server/internal/...` 包，而仓库根目录没有 `go.mod`，
> Go 的 `internal` 导入规则也不允许根目录代码引用 server 的 internal 包。
> 若确实需要根目录路径，需要在 `tests/load/` 下新建独立 module 并 `replace` 到 `server`。

运行（依赖本地 compose 的 MySQL/Redis）：

```powershell
cd server
go test -tags=integration ./tests/load -v -count=1
```

两个用例：

| 用例 | 内容 |
|---|---|
| `TestRaceReleaseVsNotify` | 50 轮同时起跑：lock 过期任务 / 支付超时任务 / HandleNotify 三路并发，逐轮记录终态与 Redis 回补次数并校验 |
| `TestReleaseVsNotifyBothTerminalStates` | 用确定的先后顺序各跑 50 轮，保证 `paid` 与 `expired+cancelled` 两条终态、以及回补 0 / 1 两种次数都被覆盖（并发时 notify 几乎总是更快） |

每轮的「回补次数」按 `remaining_after − remaining_before` 显式计算，只允许 0 或 1，
并断言与终态一一对应（`paid ↔ 0`、`expired ↔ 1`）、对账等式成立。实测：

```text
50 轮同时起跑：paid=50（回补 0 次），expired+cancelled=0（回补 1 次）
回补次数分布：0 次=50，1 次=0，>1 次或负数=0；非法终态=0
支付超时先到：50 轮终态均为 expired，Redis 回补次数均为 1
回调先到：50 轮终态均为 paid，Redis 回补次数均为 0
```

说明：`pending_payment` 订单的 lock 已是 `consumed`（状态机不变式），
lock 过期任务按 `status='active'` 过滤不会扫到它——用例仍并发生成该任务，
并断言它不会回收已消费的锁；真正会回补名额并参与竞争的是支付超时任务。

---

# 混合场景压测（mixed）

持续 3 分钟，按 60% / 20% / 20% 分配 VU：

| 场景 | 行为 |
|---|---|
| chain（60%） | 锁定 → 提交报名 → Mock 回调支付成功 |
| abandon（20%） | 锁定 → 等待 16 秒（LOCK_TTL 已压缩为 10 秒）→ 不提交，触发超时释放 |
| duplicate（20%） | 对同一订单并发重复回调 5 次 |

## 文件与运行

| 文件 | 作用 |
|---|---|
| `mixed.js` | k6 混合场景脚本 |
| `run-mixed.ps1` | 运行器：临时把 `LOCK_TTL_SECONDS` 设为 10 并重启 api → 建专用赛事/组别 → 预置用户 → 跑 k6 → `go run ./cmd/reconcile` → 锁泄漏/超时遗漏检查 → 可选复位并恢复 TTL |

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File tests\load\run-mixed.ps1 -UseDocker -ResetAfter

# 10 秒冒烟
powershell -NoProfile -ExecutionPolicy Bypass -File tests\load\run-mixed.ps1 -UseDocker -Duration 10s -ChainUserPool 2000 -TotalSlots 3000 -ResetAfter
```

`LOCK_TTL_SECONDS` 是本次新增的可选配置（>0 时优先于 `LOCK_TTL_MINUTES`），
compose 已透传该变量；运行器结束时会把 api 恢复为默认 15 分钟。

## 输出

运行器最后原样打印 `go run ./cmd/reconcile` 的输出，并给出：

```text
锁泄漏数量（active 且 expires_at 已过期超过 1 分钟）      : N（本组 M）
超时遗漏数量（pending_payment 超过 15 分钟未释放）        : K（本组 J）
```

## 已知发现（2026-10-25）

多轮 3 分钟压测中，`chain` 场景的 submit 偶发 500（耗时约 5.0 秒，正好是 MySQL DSN 的
`ReadTimeout=5s`），每出现一次失败，就可能有对应的 lock 停留在 `active` 且超期 >1 分钟：

- 泄漏的锁 100% 属于 chain 用户，且 `updated_at = created_at`（过期任务从未更新过它）；
- 同时刻 `innodb_trx` / `data_locks` / `data_lock_waits` 均无长事务与行锁，
  说明锁最终会被释放（API 重启后下一轮 tick 即可回收），属于**延迟释放**而非永久泄漏；
- 推断：submit 事务在查询超时（5s）后连接/事务未立即清理，行被短暂持有，
  过期任务的 `FOR UPDATE SKIP LOCKED` 跳过该行，直到下一轮才可能回收。

建议后续修复方向：查询超时后确保事务尽快回滚/连接销毁；或为过期任务增加
“按 id 兜底重扫”的路径，避免单靠 `SKIP LOCKED` 被长事务拖住。

**2026-10-25 已修复**：`Scheduler` 拆分调度——lock 过期任务改为**每 15 秒**执行
（支付超时仍每分钟、对账仍每 5 分钟）；job 单实例锁 TTL 为 60s，大于 15s 间隔，
异常实例最多阻塞 4 个周期。重跑本场景后：

```text
锁泄漏数量（active 且 expires_at 已过期超过 1 分钟）: 0
最大超期秒数: 0
reconcile ok: 2 group(s)   # 全部 OK，difference=0
```

api 日志中 `lock-expiry job finished` 的时间点间隔为 15.0–15.2 秒，确认调度已生效。





