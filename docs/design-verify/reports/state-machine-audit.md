# 状态机审计报告

> 由 `docs/design-verify/enumerate_state_machine.py` 自动生成，数据源 [`docs/state-machine.md`](../../state-machine.md)；生成时间 2026-10-25 14:20:26 +0800。

## 概览

| 项目 | 值 |
| --- | --- |
| 状态机 | 3 台（Lock / Order / Enrollment） |
| 状态 | 17 个（含 3 个初始伪状态「（无）」） |
| 事件 | 14 个 |
| (状态 × 事件) 组合 | 84 个 |
| 合法迁移 | 15 条 |
| 应被拒绝 | 69 条（82.1%） |
| 死锁状态 | 无 |
| 孤立状态 | 无 |

## 口径

1. 状态集合 = 迁移表「当前状态 / 目标状态」里出现过的全部值，外加初始伪状态「（无）」（表示实体尚未创建）。
2. 事件集合按实体归集：同一业务动作（如支付回调）在 Order 表和 Enrollment 表里各算一个事件；文档要求二者在同一个数据库事务内迁移（constraints.md L26-27），但状态机仍分别枚举。
3. 合法性只认迁移表：本文档开篇总则第 1 条明确“任何状态迁移未在本文档中出现，一律视为非法，须在代码中拒绝”。
4. 终态取自各表「终态」行（禁止任何出边）；标注为“迭代二”的迁移参与完整口径枚举，并单独以 MVP 口径复核终点与可达性。
5. 初始伪状态「（无）」不参与死锁判定（它不是实体状态，且必然有创建出边）。

## 事件图例

| 实体 | 事件 | 触发事件原文 | 文档行 |
| --- | --- | --- | --- |
| Lock | `E1` | POST /enrollments/lock 且 Redis Lua 原子扣减成功 | L68 |
| Lock | `E2` | POST /enrollments 提交成功 | L69 |
| Lock | `E3` | 定时任务扫描到 expires_at <= now | L70 |
| Order | `E1` | 提交报名成功 | L171 |
| Order | `E2` | 微信支付回调（验签通过、金额一致） | L172 |
| Order | `E3` | 定时任务扫描到 created_at + 15min < now | L173 |
| Order | `E4` | 退款申请受理 | L174 |
| Order | `E5` | 退款回调 REFUND.SUCCESS | L175 |
| Order | `E6` | 退款回调 REFUND.CLOSED / REFUND.ABNORMAL | L176 |
| Enrollment | `E1` | 提交报名成功 | L341 |
| Enrollment | `E2` | 支付回调（与 Order 同事务） | L342 |
| Enrollment | `E3` | 支付超时（与 Order 同事务） | L343 |
| Enrollment | `E4` | 审核通过（MVP 自动确认） | L344 |
| Enrollment | `E5` | 退款成功（Order 同事务联动） | L345 |

## 1. 合法迁移（15 条）

| # | 实体 | 当前状态 | 事件 | 目标状态 | 幂等键 | 执行方 | 副作用 | 文档行 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | Lock | `（无）` | `E1` | `active` | lock_token | 报名接口 | 写 enrollment_locks，expires_at = now + 15min；写 lock:{token} 辅助 TTL | L68 |
| 2 | Lock | `active` | `E2` | `consumed` | lock_token | 报名接口 | 事务内条件更新为 consumed，不碰 Redis | L69 |
| 3 | Lock | `active` | `E3` | `expired` | lock_token | Lock 过期任务（每分钟） | 仅当 affected_rows = 1 时 INCR group:{group_id}:remaining | L70 |
| 4 | Order | `（无）` | `E1` | `pending_payment` | order_id | 报名接口 | 与 Enrollment 同事务创建 | L171 |
| 5 | Order | `pending_payment` | `E2` | `paid` | 微信 transaction_id | 支付回调 | 同事务内 Enrollment→paid；不碰 Redis | L172 |
| 6 | Order | `pending_payment` | `E3` | `expired` | order_id | 支付超时任务（每分钟） | 同事务内 Enrollment→cancelled；Redis INCR 回补 | L173 |
| 7 | Order | `paid` | `E4` | `refunding` | out_refund_no | 退款接口 | 创建 refunds 记录；Order→refunding；**不碰 Redis** | L174 |
| 8 | Order | `refunding` | `E5` | `refunded` | refund_id | 退款回调 | 同事务内 Enrollment→refunded；Order 条件更新 affected_rows=1 时 Redis INCR **恰好一次** | L175 |
| 9 | Order | `refunding` | `E6` | `refund_failed` | refund_id | 退款回调 | 更新 refunds.status 与 Order；Enrollment 不变；**不碰 Redis** | L176 |
| 10 | Enrollment | `（无）` | `E1` | `pending_payment` | enrollment_id | 报名接口 | 与 Order 同事务创建 | L341 |
| 11 | Enrollment | `pending_payment` | `E2` | `paid` | 微信 transaction_id | 支付回调 | 仅由 Order 迁移驱动 | L342 |
| 12 | Enrollment | `pending_payment` | `E3` | `cancelled` | order_id | 支付超时任务 | 仅由 Order 迁移驱动 | L343 |
| 13 | Enrollment | `paid` | `E4` | `confirmed` | enrollment_id | 后台或确认任务 | 条件更新 WHERE status='paid'，重复执行无副作用 | L344 |
| 14 | Enrollment | `paid` | `E5` | `refunded` | refund_id | 退款回调 | 仅由 Order refunded 驱动；名额回补由 Order 退款事务统一执行 | L345 |
| 15 | Enrollment | `confirmed` | `E5` | `refunded` | refund_id | 退款回调 | 仅由 Order refunded 驱动；名额回补由 Order 退款事务统一执行 | L346 |

## 2. 应被拒绝的组合（69 / 84）

原因码：**R1** 终态不可迁出 ｜ **R2** 迁移表未定义 ｜ **R3** 实体尚未创建 ｜ **R4** 迭代二未实现。

统计：R1 34 条，R2 24 条，R3 11 条，R4 0 条

### 2.1 组合矩阵

**Lock**（三、Lock 状态机）

```
          E1  E2  E3
--------  --  --  --
（无）    ✓   ·   ·
active    ·   ✓   ✓
consumed  ·   ·   ·
expired   ·   ·   ·
```

事件：E1 = POST /enrollments/lock 且 Redis Lua 原子扣减成功；E2 = POST /enrollments 提交成功；E3 = 定时任务扫描到 expires_at <= now

**Order**（四、Order 状态机）

```
                 E1  E2  E3  E4  E5  E6
---------------  --  --  --  --  --  --
（无）           ✓   ·   ·   ·   ·   ·
pending_payment  ·   ✓   ✓   ·   ·   ·
paid             ·   ·   ·   ✓   ·   ·
expired          ·   ·   ·   ·   ·   ·
refunding        ·   ·   ·   ·   ✓   ✓
refunded         ·   ·   ·   ·   ·   ·
refund_failed    ·   ·   ·   ·   ·   ·
```

事件：E1 = 提交报名成功；E2 = 微信支付回调（验签通过、金额一致）；E3 = 定时任务扫描到 created_at + 15min < now；E4 = 退款申请受理；E5 = 退款回调 REFUND.SUCCESS；E6 = 退款回调 REFUND.CLOSED / REFUND.ABNORMAL

**Enrollment**（五、Enrollment 状态机）

```
                 E1  E2  E3  E4  E5
---------------  --  --  --  --  --
（无）           ✓   ·   ·   ·   ·
pending_payment  ·   ✓   ✓   ·   ·
paid             ·   ·   ·   ✓   ✓
cancelled        ·   ·   ·   ·   ·
confirmed        ·   ·   ·   ·   ✓
refunded         ·   ·   ·   ·   ·
```

事件：E1 = 提交报名成功；E2 = 支付回调（与 Order 同事务）；E3 = 支付超时（与 Order 同事务）；E4 = 审核通过（MVP 自动确认）；E5 = 退款成功（Order 同事务联动）

### 2.2 逐条清单

| # | 实体 | 当前状态 | 事件 | 码 | 原因 | 文档正文补充 |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | Lock | `（无）` | `E2` | R3 | 实体不存在，事件无作用对象 | — |
| 2 | Lock | `（无）` | `E3` | R3 | 实体不存在，事件无作用对象 | — |
| 3 | Lock | `active` | `E1` | R2 | 迁移表未定义（开篇总则：未列出即非法） | — |
| 4 | Lock | `consumed` | `E1` | R1 | 终态不可迁出（L71） | — |
| 5 | Lock | `consumed` | `E2` | R1 | 终态不可迁出（L71） | UPDATE ... WHERE status='active' AND expires_at > NOW() 命中 0 行 → 返回 LOCK_EXPIRED，不创建 Enrollment（§3.3 提交报名伪代码） |
| 6 | Lock | `consumed` | `E3` | R1 | 终态不可迁出（L71） | 扫描条件 WHERE status='active'，命中 0 行 → 跳过，不回补 Redis（§3.4 Lock 过期任务伪代码） |
| 7 | Lock | `expired` | `E1` | R1 | 终态不可迁出（L71） | — |
| 8 | Lock | `expired` | `E2` | R1 | 终态不可迁出（L71） | UPDATE ... WHERE status='active' AND expires_at > NOW() 命中 0 行 → 返回 LOCK_EXPIRED，不创建 Enrollment（§3.3 提交报名伪代码） |
| 9 | Lock | `expired` | `E3` | R1 | 终态不可迁出（L71） | 扫描条件 WHERE status='active'，命中 0 行 → 跳过，不回补 Redis（§3.4 Lock 过期任务伪代码） |
| 10 | Order | `（无）` | `E2` | R3 | 实体不存在，事件无作用对象 | — |
| 11 | Order | `（无）` | `E3` | R3 | 实体不存在，事件无作用对象 | — |
| 12 | Order | `（无）` | `E4` | R3 | 实体不存在，事件无作用对象 | — |
| 13 | Order | `（无）` | `E5` | R3 | 实体不存在，事件无作用对象 | — |
| 14 | Order | `（无）` | `E6` | R3 | 实体不存在，事件无作用对象 | — |
| 15 | Order | `pending_payment` | `E1` | R2 | 迁移表未定义（开篇总则：未列出即非法） | — |
| 16 | Order | `pending_payment` | `E4` | R2 | 迁移表未定义（开篇总则：未列出即非法） | 退款只允许 paid→refunding；refunding 收到 REFUND.SUCCESS 才进入 refunded 并回补 Redis，REFUND.CLOSED/ABNORMAL 进入 refund_failed；其它状态拒绝或幂等返回，不回补（§4.4 退款流程伪代码） |
| 17 | Order | `pending_payment` | `E5` | R2 | 迁移表未定义（开篇总则：未列出即非法） | 退款只允许 paid→refunding；refunding 收到 REFUND.SUCCESS 才进入 refunded 并回补 Redis，REFUND.CLOSED/ABNORMAL 进入 refund_failed；其它状态拒绝或幂等返回，不回补（§4.4 退款流程伪代码） |
| 18 | Order | `pending_payment` | `E6` | R2 | 迁移表未定义（开篇总则：未列出即非法） | 退款只允许 paid→refunding；refunding 收到 REFUND.SUCCESS 才进入 refunded 并回补 Redis，REFUND.CLOSED/ABNORMAL 进入 refund_failed；其它状态拒绝或幂等返回，不回补（§4.4 退款流程伪代码） |
| 19 | Order | `paid` | `E1` | R2 | 迁移表未定义（开篇总则：未列出即非法） | — |
| 20 | Order | `paid` | `E2` | R2 | 迁移表未定义（开篇总则：未列出即非法） | 重复回调：事务内先查状态，非 pending_payment 直接 COMMIT 返回 200，不 UPDATE、不碰 Redis（§4.2 支付回调伪代码 步骤 2） |
| 21 | Order | `paid` | `E3` | R2 | 迁移表未定义（开篇总则：未列出即非法） | 更新条件 WHERE status='pending_payment'，affected_rows=0 后跳过，不回补 Redis（§4.3 支付超时任务伪代码） |
| 22 | Order | `paid` | `E5` | R2 | 迁移表未定义（开篇总则：未列出即非法） | — |
| 23 | Order | `paid` | `E6` | R2 | 迁移表未定义（开篇总则：未列出即非法） | — |
| 24 | Order | `expired` | `E1` | R1 | 终态不可迁出（L179） | — |
| 25 | Order | `expired` | `E2` | R1 | 终态不可迁出（L179） | 重复回调：事务内先查状态，非 pending_payment 直接 COMMIT 返回 200，不 UPDATE、不碰 Redis（§4.2 支付回调伪代码 步骤 2） |
| 26 | Order | `expired` | `E3` | R1 | 终态不可迁出（L179） | 更新条件 WHERE status='pending_payment'，affected_rows=0 后跳过，不回补 Redis（§4.3 支付超时任务伪代码） |
| 27 | Order | `expired` | `E4` | R1 | 终态不可迁出（L179） | 退款只允许 paid→refunding；refunding 收到 REFUND.SUCCESS 才进入 refunded 并回补 Redis，REFUND.CLOSED/ABNORMAL 进入 refund_failed；其它状态拒绝或幂等返回，不回补（§4.4 退款流程伪代码） |
| 28 | Order | `expired` | `E5` | R1 | 终态不可迁出（L179） | 退款只允许 paid→refunding；refunding 收到 REFUND.SUCCESS 才进入 refunded 并回补 Redis，REFUND.CLOSED/ABNORMAL 进入 refund_failed；其它状态拒绝或幂等返回，不回补（§4.4 退款流程伪代码） |
| 29 | Order | `expired` | `E6` | R1 | 终态不可迁出（L179） | 退款只允许 paid→refunding；refunding 收到 REFUND.SUCCESS 才进入 refunded 并回补 Redis，REFUND.CLOSED/ABNORMAL 进入 refund_failed；其它状态拒绝或幂等返回，不回补（§4.4 退款流程伪代码） |
| 30 | Order | `refunding` | `E1` | R2 | 迁移表未定义（开篇总则：未列出即非法） | — |
| 31 | Order | `refunding` | `E2` | R2 | 迁移表未定义（开篇总则：未列出即非法） | — |
| 32 | Order | `refunding` | `E3` | R2 | 迁移表未定义（开篇总则：未列出即非法） | — |
| 33 | Order | `refunding` | `E4` | R2 | 迁移表未定义（开篇总则：未列出即非法） | 退款只允许 paid→refunding；refunding 收到 REFUND.SUCCESS 才进入 refunded 并回补 Redis，REFUND.CLOSED/ABNORMAL 进入 refund_failed；其它状态拒绝或幂等返回，不回补（§4.4 退款流程伪代码） |
| 34 | Order | `refunded` | `E1` | R1 | 终态不可迁出（L177） | — |
| 35 | Order | `refunded` | `E2` | R1 | 终态不可迁出（L177） | 重复回调：事务内先查状态，非 pending_payment 直接 COMMIT 返回 200，不 UPDATE、不碰 Redis（§4.2 支付回调伪代码 步骤 2） |
| 36 | Order | `refunded` | `E3` | R1 | 终态不可迁出（L177） | 更新条件 WHERE status='pending_payment'，affected_rows=0 后跳过，不回补 Redis（§4.3 支付超时任务伪代码） |
| 37 | Order | `refunded` | `E4` | R1 | 终态不可迁出（L177） | 退款只允许 paid→refunding；refunding 收到 REFUND.SUCCESS 才进入 refunded 并回补 Redis，REFUND.CLOSED/ABNORMAL 进入 refund_failed；其它状态拒绝或幂等返回，不回补（§4.4 退款流程伪代码） |
| 38 | Order | `refunded` | `E5` | R1 | 终态不可迁出（L177） | 退款只允许 paid→refunding；refunding 收到 REFUND.SUCCESS 才进入 refunded 并回补 Redis，REFUND.CLOSED/ABNORMAL 进入 refund_failed；其它状态拒绝或幂等返回，不回补（§4.4 退款流程伪代码） |
| 39 | Order | `refunded` | `E6` | R1 | 终态不可迁出（L177） | 退款只允许 paid→refunding；refunding 收到 REFUND.SUCCESS 才进入 refunded 并回补 Redis，REFUND.CLOSED/ABNORMAL 进入 refund_failed；其它状态拒绝或幂等返回，不回补（§4.4 退款流程伪代码） |
| 40 | Order | `refund_failed` | `E1` | R1 | 终态不可迁出（L178） | — |
| 41 | Order | `refund_failed` | `E2` | R1 | 终态不可迁出（L178） | — |
| 42 | Order | `refund_failed` | `E3` | R1 | 终态不可迁出（L178） | — |
| 43 | Order | `refund_failed` | `E4` | R1 | 终态不可迁出（L178） | 退款只允许 paid→refunding；refunding 收到 REFUND.SUCCESS 才进入 refunded 并回补 Redis，REFUND.CLOSED/ABNORMAL 进入 refund_failed；其它状态拒绝或幂等返回，不回补（§4.4 退款流程伪代码） |
| 44 | Order | `refund_failed` | `E5` | R1 | 终态不可迁出（L178） | 退款只允许 paid→refunding；refunding 收到 REFUND.SUCCESS 才进入 refunded 并回补 Redis，REFUND.CLOSED/ABNORMAL 进入 refund_failed；其它状态拒绝或幂等返回，不回补（§4.4 退款流程伪代码） |
| 45 | Order | `refund_failed` | `E6` | R1 | 终态不可迁出（L178） | 退款只允许 paid→refunding；refunding 收到 REFUND.SUCCESS 才进入 refunded 并回补 Redis，REFUND.CLOSED/ABNORMAL 进入 refund_failed；其它状态拒绝或幂等返回，不回补（§4.4 退款流程伪代码） |
| 46 | Enrollment | `（无）` | `E2` | R3 | 实体不存在，事件无作用对象 | — |
| 47 | Enrollment | `（无）` | `E3` | R3 | 实体不存在，事件无作用对象 | — |
| 48 | Enrollment | `（无）` | `E4` | R3 | 实体不存在，事件无作用对象 | — |
| 49 | Enrollment | `（无）` | `E5` | R3 | 实体不存在，事件无作用对象 | — |
| 50 | Enrollment | `pending_payment` | `E1` | R2 | 迁移表未定义（开篇总则：未列出即非法） | — |
| 51 | Enrollment | `pending_payment` | `E4` | R2 | 迁移表未定义（开篇总则：未列出即非法） | — |
| 52 | Enrollment | `pending_payment` | `E5` | R2 | 迁移表未定义（开篇总则：未列出即非法） | 退款只允许 paid/confirmed→refunded，且必须由 Order refunded 事务联动；其它状态拒绝，避免重复回补（§5.3 退款联动要求） |
| 53 | Enrollment | `paid` | `E1` | R2 | 迁移表未定义（开篇总则：未列出即非法） | — |
| 54 | Enrollment | `paid` | `E2` | R2 | 迁移表未定义（开篇总则：未列出即非法） | — |
| 55 | Enrollment | `paid` | `E3` | R2 | 迁移表未定义（开篇总则：未列出即非法） | — |
| 56 | Enrollment | `cancelled` | `E1` | R1 | 终态不可迁出（L347） | — |
| 57 | Enrollment | `cancelled` | `E2` | R1 | 终态不可迁出（L347） | — |
| 58 | Enrollment | `cancelled` | `E3` | R1 | 终态不可迁出（L347） | — |
| 59 | Enrollment | `cancelled` | `E4` | R1 | 终态不可迁出（L347） | — |
| 60 | Enrollment | `cancelled` | `E5` | R1 | 终态不可迁出（L347） | 退款只允许 paid/confirmed→refunded，且必须由 Order refunded 事务联动；其它状态拒绝，避免重复回补（§5.3 退款联动要求） |
| 61 | Enrollment | `confirmed` | `E1` | R2 | 迁移表未定义（开篇总则：未列出即非法） | — |
| 62 | Enrollment | `confirmed` | `E2` | R2 | 迁移表未定义（开篇总则：未列出即非法） | — |
| 63 | Enrollment | `confirmed` | `E3` | R2 | 迁移表未定义（开篇总则：未列出即非法） | — |
| 64 | Enrollment | `confirmed` | `E4` | R2 | 迁移表未定义（开篇总则：未列出即非法） | — |
| 65 | Enrollment | `refunded` | `E1` | R1 | 终态不可迁出（L347） | — |
| 66 | Enrollment | `refunded` | `E2` | R1 | 终态不可迁出（L347） | — |
| 67 | Enrollment | `refunded` | `E3` | R1 | 终态不可迁出（L347） | — |
| 68 | Enrollment | `refunded` | `E4` | R1 | 终态不可迁出（L347） | — |
| 69 | Enrollment | `refunded` | `E5` | R1 | 终态不可迁出（L347） | 退款只允许 paid/confirmed→refunded，且必须由 Order refunded 事务联动；其它状态拒绝，避免重复回补（§5.3 退款联动要求） |

## 3. 死锁检查

口径：非终态、且没有任何一条合法出边。「（无）」是初始伪状态，不参与判定。

| 实体 | 非终态状态 | 出度 | 合法出边 |
| --- | --- | --- | --- |
| Lock | `active` | 2 | E2→consumed、E3→expired |
| Order | `pending_payment` | 2 | E2→paid、E3→expired |
| Order | `paid` | 1 | E4→refunding |
| Order | `refunding` | 2 | E5→refunded、E6→refund_failed |
| Enrollment | `pending_payment` | 2 | E2→paid、E3→cancelled |
| Enrollment | `paid` | 2 | E4→confirmed、E5→refunded |
| Enrollment | `confirmed` | 1 | E5→refunded |

**结论：**✅ 不存在死锁状态。

## 4. 孤立状态检查

从初始状态「（无）」做 BFS，按迁移表的有向边展开。

| 实体 | 状态 | 可达性 | 最短路径（事件序列） |
| --- | --- | --- | --- |
| Lock | `active` | ✅ 可达 | `（无） -E1-> active` |
| Lock | `consumed` | ✅ 可达 | `（无） -E1-> active -E2-> consumed` |
| Lock | `expired` | ✅ 可达 | `（无） -E1-> active -E3-> expired` |
| Order | `pending_payment` | ✅ 可达 | `（无） -E1-> pending_payment` |
| Order | `paid` | ✅ 可达 | `（无） -E1-> pending_payment -E2-> paid` |
| Order | `expired` | ✅ 可达 | `（无） -E1-> pending_payment -E3-> expired` |
| Order | `refunding` | ✅ 可达 | `（无） -E1-> pending_payment -E2-> paid -E4-> refunding` |
| Order | `refunded` | ✅ 可达 | `（无） -E1-> pending_payment -E2-> paid -E4-> refunding -E5-> refunded` |
| Order | `refund_failed` | ✅ 可达 | `（无） -E1-> pending_payment -E2-> paid -E4-> refunding -E6-> refund_failed` |
| Enrollment | `pending_payment` | ✅ 可达 | `（无） -E1-> pending_payment` |
| Enrollment | `paid` | ✅ 可达 | `（无） -E1-> pending_payment -E2-> paid` |
| Enrollment | `cancelled` | ✅ 可达 | `（无） -E1-> pending_payment -E3-> cancelled` |
| Enrollment | `confirmed` | ✅ 可达 | `（无） -E1-> pending_payment -E2-> paid -E4-> confirmed` |
| Enrollment | `refunded` | ✅ 可达 | `（无） -E1-> pending_payment -E2-> paid -E5-> refunded` |

**结论：**✅ 不存在从初始状态不可达的孤立状态，全部状态可达。

## 5. 结论与告警

## 6. MVP 口径（排除“迭代二”退款边）

| 实体 | 合法迁移 | 应拒绝 | MVP 终点（无 MVP 出边） | 迭代二才可达 |
| --- | --- | --- | --- | --- |
| Lock | 3 | 9 | 无 | 无 |
| Order | 6 | 36 | 无 | 无 |
| Enrollment | 6 | 24 | 无 | 无 |

- 退款迁移已纳入本次实现：Order `paid→refunding→refunded/refund_failed`；Enrollment `paid/confirmed→refunded` 仅在 Order `refunded` 事务内联动。
- 当前迁移表不再包含标注“迭代二”的预留边；后台手动确认接口仍未定义。

