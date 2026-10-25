# 压测报告（四场景）

- 日期：2026-10-25
- 环境：本地 `docker compose`（MySQL 8.0 / Redis 7 / Go API），压测工具 k6 v2.3.0（容器 `grafana/k6:latest`）
- 数据：压测专用赛事/组别（`price=1` 分、`total_slots` 按场景设定），每轮结束清理
- 总体结论：4 个场景全部通过；其中「混合负载」首轮暴露锁延迟释放问题，修复（扫描间隔改为 15 秒）后复测通过

| 场景 | 目标 | 关键数字 | 通过/未通过 | 未通过时的发现 |
|---|---|---|---|---|
| **1. 抢名额**（`tests/load/lock-oversell.js`） | 2000 次并发锁定不超卖；成功数 ≤ `total_slots`，且 `remaining + active locks = total_slots` | 200 VU × 10 次 = 2000 请求；HTTP **200=100 / 409=1900 / 其它=0**；Redis remaining `100 → 0`；MySQL active lock **100**；对账 `0 + 0 + 0 + 100 = 100`；耗时 603ms，p95 124ms | ✅ 通过 | — |
| **2. 支付回调幂等**（`tests/load/notify-idempotent.js`） | 同一订单 10 次并发重复回调，只发生一次状态迁移；Redis remaining 不变 | 10 单 × 10 次 = 100 回调，**200=100**；每单 `applied=1 / duplicate=9`；每单 **10 个互不相同的 TransactionID**；落库 `transaction_id` 属于回调集合；Redis remaining **90 → 90**；Order 10/10 `paid`，`updated_at = paid_at` | ✅ 通过 | 首次执行时 mock 回调返回 404：`/internal/mock-pay/*` 会校验订单所属用户存在，而压测用户未落库；运行器改为预置用户后通过（非幂等缺陷） |
| **3. 释放 vs 回调竞态**（`server/tests/load/race_release_vs_notify_test.go`） | 释放任务与支付回调并发，终态只能 `paid` 或 `expired+cancelled`；回补次数只能 0 或 1；对账等式每轮成立 | 150 轮（50 同时起跑 + 50 超时先到 + 50 回调先到）；`paid=100`、`expired+cancelled=50`；回补 **0 次=100 轮 / 1 次=50 轮**，>1 或负数 **0**；非法终态 **0**；每轮对账成立 | ✅ 通过 | — |
| **4. 混合负载**（`tests/load/mixed.js`，3 分钟 60/20/20） | 对账无差异、无锁泄漏（`active` 且过期 >1 分钟）、无支付超时遗漏 | 修复后：`reconcile ok: 2 group(s)`（`difference=0`）；锁泄漏 **4 → 0**；超时遗漏 **0**；chain lock=4352 / submit=4349 / notify=4349；abandon 120 次锁定；duplicate 42855 次回调；总请求 56068，193s；`lock-expiry` 间隔 15.0–15.2s | ✅ 通过（修复后） | 首轮锁泄漏 4 条（`active` 且过期 112–163s），均为 chain 用户、`updated_at = created_at`，与 submit 500（5002–5015ms ≈ MySQL DSN `ReadTimeout=5s`）一一对应；根因：`SKIP LOCKED` 跳过被 submit 事务（最长 5s）持有的行，原 60s 扫描间隔造成延迟窗口；修复：扫描间隔改为 15s |

## 场景 4 详情

- **根因**：`SKIP LOCKED` 跳过被 submit 事务（最长 5s）持有的行，原 60s 扫描间隔造成延迟窗口。
- **修复**：扫描间隔改为 15s（已验证）。
- **遗留项**：submit 事务边界优化（将非 MySQL 操作移出事务，目标行锁持有时间降至毫秒级），列为上线前完成项。
- **监控项**：新增“过期 >30s 仍 active 的 lock 数”指标，阈值 10 条告警，写入运维检查清单。

## 复现命令

```powershell
# 1) 抢名额
powershell -File tests\load\run-lock-oversell.ps1 -UseDocker -ResetAfter

# 2) 支付回调幂等
powershell -File tests\load\run-notify-idempotent.ps1 -UseDocker -ResetAfter

# 3) 释放 vs 回调竞态（Go 集成测试）
cd server
go test -tags=integration ./tests/load -v -count=1

# 4) 混合场景（加 -Duration 10s 可做冒烟）
powershell -File tests\load\run-mixed.ps1 -UseDocker -ResetAfter
```

## 压测后环境

四个场景结束后已清理：`events=0`、`groups=0`、`enrollment_locks/enrollments/orders/disclaimer_signatures/refunds=0`、
Redis `DBSIZE=0`、`users=8`（压测前已有账号）；`go run ./cmd/reconcile` 输出 `no groups to reconcile`。
