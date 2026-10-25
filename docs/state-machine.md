> 本文件于 2026-10-25 冻结。后续任何状态迁移变更，须经人工批准并同步更新 openapi.yaml。
>
> 2026-10-25 经人工批准补充退款状态机：Order `paid → refunding → refunded / refund_failed`；
> Enrollment 仅随 Order `refunded` 联动；Redis 回补仅由 `REFUND.SUCCESS` 触发。

# 名额与订单状态机

本文档是并发、支付、超时释放相关代码的唯一基准。

**总则**

1. 任何未在本文档迁移表中出现的状态迁移一律非法，代码必须拒绝，并返回 `openapi.yaml` 中定义的错误码。
2. `终态` 表示**禁止任何出边**；终态记录不得再被任何任务或接口更新。
3. 迁移表同时包含本次实现与标注“迭代二”的预留迁移。迭代二迁移在功能上线前不可达，
   但**不得从状态机中删除**。退款迁移已纳入本次实现；后台手动确认接口仍为迭代二预留。
4. 状态迁移与 Redis 名额回补必须满足本文档“先条件更新、后看 affected_rows”的顺序，
   禁止先回补再更新。

---

## 一、实体与存储分布

| 实体 | 存储位置 | 键 / 表 | 说明 |
|---|---|---|---|
| Group 名额计数器 | Redis | `group:{group_id}:remaining` | 唯一实时权威值 |
| Group 名额快照 | MySQL | `groups.total_slots` | 仅作配置与对账基准 |
| Lock 记录 | MySQL | `enrollment_locks` | 锁定的持久化记录，状态权威 |
| Lock 辅助 TTL | Redis | `lock:{lock_token}` | 仅辅助过期判断，不作为状态依据 |
| Lock 回补幂等键 | Redis | `lock:rollback:{lock_token}` | 回补时 `SET NX PX 86400000`，防同一 token 重复 `INCR` |
| 用户串行化锁 | Redis | `lock:{user_id}:{group_id}` | `SET NX PX 15000`，防同一用户并发锁定 |
| Enrollment | MySQL | `enrollments` | 报名记录，承载名额占用 |
| Order | MySQL | `orders` | 支付订单 |
| Refund | MySQL | `refunds` | 退款申请记录；`out_refund_no` 唯一，`refund_id` 唯一；申请与回调幂等键 |
| 免责协议签署 | MySQL | `disclaimer_signatures` | 只允许 INSERT，不允许 UPDATE |

**关键原则**：剩余名额的实时值只认 Redis。MySQL 中的 `total_slots` 是配置值，
`remaining_slots = total_slots - 活跃锁定名额 - 待支付名额 - 已支付/已确认名额`，对账脚本按此公式验证。

---

## 二、状态与名额占用口径

名额在 Lock 阶段由 Redis 预减，之后通过状态迁移转移占用，**不重复计数**。

| 实体 | 状态 | 是否占用名额 | 说明 |
|---|---|---|---|
| Lock | `active` | 是 | Redis 已预减，等待提交报名 |
| Lock | `consumed` | 是（转移） | 占用转移给同组别的 `pending_payment` Enrollment，Lock 行本身不再计数 |
| Lock | `expired` | 否 | 已回补 Redis |
| Order | `pending_payment` / `paid` / `refunding` / `refunded` / `refund_failed` / `expired` | 不单独计数 | 名额占用以 Enrollment 为准，Order 不重复计入等式 |
| Enrollment | `pending_payment` | 是 | 已消费锁，等待支付 |
| Enrollment | `paid` | 是 | 已支付，未确认 |
| Enrollment | `confirmed` | 是 | 已确认，仍占名额 |
| Enrollment | `cancelled` | 否 | 超时取消，已回补 Redis |
| Enrollment | `refunded` | 否 | 退款完成，已回补 Redis |

**不变式**：同一报名在任意时刻只可能由 `active` Lock 或 `pending_payment` Enrollment 之一占用名额；
`active → consumed` 与 `INSERT enrollment` 在同一事务内完成，因此不存在两边同时计数的窗口。

### 2.1 赛事生命周期与报名状态

`events` 已拆分两个维度，报名状态不落库、由函数实时推导：

| 维度 | 字段 | 取值 | 说明 |
|---|---|---|---|
| 生命周期 | `lifecycle_status` | `draft` / `published` / `finished` / `cancelled` | 原 `status` 字段重命名；历史 `registration_open` / `registration_closed` 迁移为 `published` |
| 报名状态 | `resolveRegistrationStatus(event, now)` | `not_started` / `open` / `closed` | 只读推导，禁止写回数据库 |

报名窗口：`registration_open_at`、`registration_deadline`（均为 NULL 表示该侧不限制）；
手动关闭：`registration_manually_closed`（默认 `false`）、`registration_closed_reason`（可空）。

赛事时间窗口不变式：`registration_open_at < registration_deadline <= start_time < end_time`。
后台创建/更新赛事违反时返回 `400 INVALID_TIME_RANGE`，message 指明具体不成立的关系；
`duration_minutes` 由后端按 `end_time - start_time` 计算并随赛事返回，前端不得重复计算。

`resolveRegistrationStatus` 固定优先级（实现见 `server/internal/model/registration_status.go`）：

1. `registration_manually_closed = true` → `closed`
2. `now < registration_open_at` → `not_started`
3. `now > registration_deadline` → `closed`
4. `lifecycle_status = cancelled` → `closed`
5. 其余 → `open`

接口与校验口径：

- `GET /events`、`GET /events/{id}` 返回 `lifecycle_status`、`registration_status`、`registration_closed_reason`；
  `GET /events?registration_status=not_started|open|closed` 的 SQL 过滤条件必须与上述优先级一致。
- `POST /enrollments/lock` 与 `POST /enrollments` 仅当 `resolveRegistrationStatus == open` 时继续，否则返回
  `409 EVENT_NOT_OPEN`；两处都必须先判断报名开关，再判断名额 / lock 有效性。
  message 按原因生成：手动关闭用 `registration_closed_reason`（空则默认文案）、已截止返回截止时间、未开始返回开放时间。
- `PATCH /admin/events/{id}/registration/close` 置 `registration_manually_closed=true` 并记录 `reason` 与审计日志；
  `PATCH /admin/events/{id}/registration/open` 置 `false` 并重新推导状态，截止时间已过时返回
  `409 REGISTRATION_DEADLINE_PASSED` 且不落库。两个接口均幂等，且**不修改任何 lock / enrollment / order**。
- 公开列表/详情只暴露 `lifecycle_status IN ('published','finished')`；`draft` / `cancelled` 不对外（返回 404）。

---

## 三、Lock 状态机

### 3.1 迁移表

| 当前状态 | 触发事件 | 目标状态 | 执行方 | 幂等键 | 副作用 |
|---|---|---|---|---|---|
| （无） | `POST /enrollments/lock` 且 Redis Lua 原子扣减成功 | `active` | 报名接口 | lock_token | 写 `enrollment_locks`，`expires_at = now + 15min`；写 `lock:{token}` 辅助 TTL |
| `active` | `POST /enrollments` 提交成功 | `consumed` | 报名接口 | lock_token | 事务内条件更新为 `consumed`，不碰 Redis |
| `active` | 定时任务扫描到 `expires_at <= now` | `expired` | Lock 过期任务（每 15 秒） | lock_token | 仅当 `affected_rows = 1` 时 `INCR group:{group_id}:remaining` |
| `consumed` / `expired` | —— | 终态 | —— | —— | 禁止任何出边；不再回补 Redis |

### 3.2 锁定接口伪代码要求（不可协商）

```text
function Lock(user_id, group_id):
    # 1) 用户级串行化：同一用户对同一组别不允许并发锁定
    ok = SET lock:{user_id}:{group_id} 1 NX PX 15000
    if not ok:
        return 429 RATE_LIMITED

    # 2) 单条 Lua 脚本内完成“remaining > 0 判断 + DECR”，禁止 GET 后 DECR
    n = EVAL lock_slot.lua KEYS=[group:{group_id}:remaining] ARGV=[1]
    if n < 0:
        return 409 GROUP_SOLD_OUT

    # 3) 落库；落库失败必须在同一请求内回补，禁止留下 active 记录
    token = new_token()
    BEGIN
        INSERT INTO enrollment_locks(token, user_id, group_id, status, expires_at)
        VALUES (token, user_id, group_id, 'active', NOW() + 15 MINUTE)
    COMMIT
    on error:
        # 幂等回补：同一 lock_token 只允许 INCR 一次
        EVAL rollback_slot.lua KEYS=[group:{group_id}:remaining, lock:rollback:{token}] ARGV=[86400000]
        return 500 INTERNAL_ERROR

    # 辅助 TTL，仅用于过期判断；Lock 状态始终以 MySQL 为准
    SET lock:{token} 1 PX 900000
    return 200 { lock_token: token, expires_at, remaining_slots: n }
```

`rollback_slot.lua` 语义：先 `SET lock:rollback:{lock_token} 1 NX PX 86400000`，仅当设置成功才
`INCR group:{group_id}:remaining`；同一 lock_token 重复调用返回 0，不重复回补。脚本必须通过
`go:embed` 加载，禁止在 Go 中拼接。

`lock_slot.lua` 语义：`if tonumber(redis.call('GET', key) or '0') <= 0 then return -1 end;
redis.call('DECR', key); return redis.call('GET', key)`。脚本必须通过 `go:embed` 加载，禁止在 Go 中拼接。

### 3.3 提交报名伪代码要求

```text
function Submit(user_id, lock_token, form):
    BEGIN TRANSACTION
        UPDATE enrollment_locks
           SET status = 'consumed', consumed_at = NOW()
         WHERE token = lock_token
           AND user_id = user_id
           AND status = 'active'
           AND expires_at > NOW()

        if affected_rows != 1:
            ROLLBACK
            # 按当前行区分：不存在/不属于本人 → LOCK_INVALID；
            # 已过期 → LOCK_EXPIRED；已消费 → LOCK_ALREADY_CONSUMED
            return 409 <对应错误码>

        INSERT INTO enrollments(status = 'pending_payment', ...)
        INSERT INTO orders(status = 'pending_payment', amount = group.price, ...)
    COMMIT

    # 不扣减、不回补 Redis：名额已在 Lock 阶段占用
    return 201 { enrollment, order }
```

同一用户对同一组别的重复报名由数据库唯一约束兜底，冲突时返回 `ENROLLMENT_DUPLICATE`。

### 3.4 Lock 过期任务伪代码要求

```text
SELECT id, group_id FROM enrollment_locks
 WHERE status = 'active' AND expires_at <= NOW()
 ORDER BY expires_at
 FOR UPDATE SKIP LOCKED
 LIMIT 200;

for each row:
    BEGIN TRANSACTION
        UPDATE enrollment_locks
           SET status = 'expired', expired_at = NOW()
         WHERE id = row.id AND status = 'active'

        if affected_rows = 1:
            INCR group:{row.group_id}:remaining
        else:
            # 已 consumed，或已被其它实例处理：跳过，绝不回补
            skip
    COMMIT
```

任务入口必须先获取单实例锁 `SET job:lock-expiry NX PX 60000`。

---

## 四、Order 状态机

### 4.1 迁移表

| 当前状态 | 触发事件 | 目标状态 | 执行方 | 幂等键 | 副作用 |
|---|---|---|---|---|---|
| （无） | 提交报名成功 | `pending_payment` | 报名接口 | order_id | 与 Enrollment 同事务创建 |
| `pending_payment` | 微信支付回调（验签通过、金额一致） | `paid` | 支付回调 | 微信 transaction_id | 同事务内 Enrollment→`paid`；不碰 Redis |
| `pending_payment` | 定时任务扫描到 `created_at + 15min < now` | `expired` | 支付超时任务（每分钟） | order_id | 同事务内 Enrollment→`cancelled`；Redis INCR 回补 |
| `paid` | 退款申请受理 | `refunding` | 退款接口 | out_refund_no | 创建 `refunds` 记录；Order→`refunding`；**不碰 Redis** |
| `refunding` | 退款回调 `REFUND.SUCCESS` | `refunded` | 退款回调 | refund_id | 同事务内 Enrollment→`refunded`；Order 条件更新 `affected_rows=1` 时 Redis INCR **恰好一次** |
| `refunding` | 退款回调 `REFUND.CLOSED` / `REFUND.ABNORMAL` | `refund_failed` | 退款回调 | refund_id | 更新 `refunds.status` 与 Order；Enrollment 不变；**不碰 Redis** |
| `refunded` | —— | 终态 | —— | —— | 禁止任何出边 |
| `refund_failed` | —— | 终态 | —— | —— | 禁止任何出边；MVP 不定义重试，如需重新申请退款必须新增迁移 |
| `expired` | —— | 终态 | —— | —— | 禁止任何出边 |

**退款口径说明**：退款已纳入本次实现。

- 正常流程必须 `paid → refunding → refunded`；
- `REFUND.CLOSED` / `REFUND.ABNORMAL` 只把 `refunding` 置为 `refund_failed`，不回补 Redis；
- 同一 `out_refund_no` 重复申请退款直接返回已有 `refunds` 记录，不产生新记录，也不重复迁移 Order；
- `refunded` 为退款成功终态，`refund_failed` 为失败终点，MVP 不定义从 `refund_failed` 重新申请的出边。

### 4.2 支付回调伪代码要求（不可协商）

```text
function OnPayNotify(headers, body):
    # 1) 先验签，失败不修改任何数据
    if not VerifyWechatPaySign(headers, body):
        return 401 PAY_SIGN_INVALID

    tx = Decrypt(resource)   # out_trade_no, transaction_id, amount.total, trade_state

    BEGIN TRANSACTION
        order = SELECT * FROM orders WHERE id = tx.out_trade_no FOR UPDATE

        if order is null:
            ROLLBACK
            LOG ALERT "unknown out_trade_no"
            return 200

        # 2) 先查状态，后迁移；重复回调直接 COMMIT 返回 200
        if order.status != 'pending_payment':
            COMMIT
            return 200

        # 3) 金额校验：不一致不迁移，告警，仍返回 200
        if tx.amount.total != order.amount:
            ROLLBACK
            LOG ALERT "PAY_AMOUNT_MISMATCH"
            return 200

        # 4) 状态迁移必须在同一事务内
        UPDATE orders
           SET status = 'paid', paid_at = NOW(), transaction_id = tx.transaction_id
         WHERE id = order.id

        UPDATE enrollments
           SET status = 'paid', paid_at = NOW()
         WHERE order_id = order.id

    COMMIT

    # 5) 通知走异步旁路；失败只记录日志，不影响回调返回
    EnqueueNotificationAsync(order.id)
    return 200
```

### 4.3 支付超时任务伪代码要求

```text
SELECT o.id, e.group_id FROM orders o
  JOIN enrollments e ON e.order_id = o.id
 WHERE o.status = 'pending_payment'
   AND o.created_at <= NOW() - INTERVAL 15 MINUTE
 ORDER BY o.created_at
 FOR UPDATE SKIP LOCKED
 LIMIT 200;

for each row:
    BEGIN TRANSACTION
        UPDATE orders
           SET status = 'expired', expired_at = NOW()
         WHERE id = row.id AND status = 'pending_payment'

        if affected_rows = 1:
            UPDATE enrollments
               SET status = 'cancelled', cancelled_at = NOW()
             WHERE order_id = row.id AND status = 'pending_payment'
            INCR group:{row.group_id}:remaining
        else:
            # 支付回调已抢先迁移：跳过，不回补
            skip
    COMMIT
```

任务入口必须先获取单实例锁 `SET job:pay-timeout NX PX 60000`。

### 4.4 退款流程伪代码要求（不可协商）

#### 申请退款

```text
# 先按 out_refund_no 幂等查询；已存在则直接返回原 RefundResult
existing = SELECT * FROM refunds WHERE out_refund_no = ?
if existing is not null:
    return existing.RefundResult

result = Provider.Refund(out_trade_no, out_refund_no, amount)

BEGIN TRANSACTION
    order = SELECT * FROM orders WHERE id = ? FOR UPDATE
    if order.status != 'paid':
        ROLLBACK; return 409 ORDER_STATUS_INVALID

    # out_refund_no 唯一键兜底：并发同单号只有一个 INSERT 成功
    INSERT INTO refunds(refund_id, order_id, out_refund_no, amount, status)
    UPDATE orders SET status = 'refunding' WHERE id = ? AND status = 'paid'
COMMIT

# 申请阶段不 INCR Redis
```

#### 退款回调

```text
BEGIN TRANSACTION
    order = SELECT * FROM orders WHERE id = tx.out_trade_no FOR UPDATE

    if order is null:
        COMMIT; LOG ALERT "unknown out_trade_no"; return 200

    if order.status = 'refunded':
        COMMIT; return 200                    # 幂等，不 UPDATE、不 INCR

    if order.status NOT IN ('paid', 'refunding'):
        COMMIT; return 200                    # 忽略，不 INCR

    # 非成功回调：只更新退款记录；已进入 refunding 的订单进入 refund_failed
    if tx.refund_status != 'SUCCESS':
        UPDATE refunds SET status = tx.refund_status WHERE out_refund_no = tx.out_refund_no
        if order.status = 'refunding':
            UPDATE orders SET status = 'refund_failed'
             WHERE id = order.id AND status = 'refunding'
        COMMIT; return 200                    # 不 INCR

    # SUCCESS：先条件更新 Order，再按 affected_rows 决定是否回补
    UPDATE orders SET status = 'refunded'
     WHERE id = order.id AND status IN ('paid', 'refunding')

    if affected_rows = 1:
        UPDATE enrollments SET status = 'refunded'
         WHERE order_id = order.id AND status IN ('paid', 'confirmed')
        INCR group:{group_id}:remaining        # 恰好一次
        UPDATE refunds SET status = 'SUCCESS' WHERE out_refund_no = tx.out_refund_no

COMMIT
return 200
```

**关键约束：**

- Redis 只在 `REFUND.SUCCESS` 且 `UPDATE orders ... affected_rows = 1` 时 `INCR`；
- `REFUND.CLOSED` / `REFUND.ABNORMAL` 不迁移 Enrollment、不 `INCR`；
- Order 已是 `refunded` 时直接返回 200，不重复 `INCR`；
- 正常流程必须先申请退款进入 `refunding`；若回调在 `paid` 状态到达（申请记录缺失/乱序），
  实现按同一条件更新兜底迁移，仍必须满足 `affected_rows = 1` 才回补。

---

## 五、Enrollment 状态机

### 5.1 迁移表

| 当前状态 | 触发事件 | 目标状态 | 执行方 | 幂等键 | 副作用 |
|---|---|---|---|---|---|
| （无） | 提交报名成功 | `pending_payment` | 报名接口 | enrollment_id | 与 Order 同事务创建 |
| `pending_payment` | 支付回调（与 Order 同事务） | `paid` | 支付回调 | 微信 transaction_id | 仅由 Order 迁移驱动 |
| `pending_payment` | 支付超时（与 Order 同事务） | `cancelled` | 支付超时任务 | order_id | 仅由 Order 迁移驱动 |
| `paid` | 审核通过（MVP 自动确认） | `confirmed` | 后台或确认任务 | enrollment_id | 条件更新 `WHERE status='paid'`，重复执行无副作用 |
| `paid` | 退款成功（Order 同事务联动） | `refunded` | 退款回调 | refund_id | 仅由 Order `refunded` 驱动；名额回补由 Order 退款事务统一执行 |
| `confirmed` | 退款成功（Order 同事务联动） | `refunded` | 退款回调 | refund_id | 仅由 Order `refunded` 驱动；名额回补由 Order 退款事务统一执行 |
| `cancelled` / `refunded` | —— | 终态 | —— | —— | 禁止任何出边；不再回补 Redis |

**退款口径说明**：`refunded` 只能由 Order 的退款成功事务联动进入；Order 处于
`refunding` / `refund_failed` 时，Enrollment 分别保持 `paid`/`confirmed`，不得单独改为
`refunded`，也不得单独回补 Redis。`confirmed` 已具备退款出边，因此不是状态机终态。

### 5.2 审核确认伪代码要求

```text
UPDATE enrollments
   SET status = 'confirmed', confirmed_at = NOW()
 WHERE id = ? AND status = 'paid'

if affected_rows = 0:
    # 已 confirmed → 幂等成功；其它状态 → 拒绝
    current = SELECT status FROM enrollments WHERE id = ?
    if current == 'confirmed': return 200
    return 409   # 确认接口属迭代二，届时在 openapi.yaml 补充专用错误码
```

MVP 由定时任务自动确认；后台手动确认接口属于迭代二，本契约不定义。

### 5.3 退款联动要求

退款不单独提供 Enrollment 接口，必须由 Order 退款成功事务统一驱动：

- 仅当 Order 条件更新为 `refunded`（`affected_rows = 1`）时，才把 Enrollment 从
  `paid` / `confirmed` 更新为 `refunded`；
- `REFUND.CLOSED` / `REFUND.ABNORMAL` 只把 Order 从 `refunding` 置为 `refund_failed`，
  Enrollment 保持不变；
- Redis 回补只由 Order 退款事务执行一次，禁止在 Enrollment 侧单独 `INCR`。

---

## 六、并发竞争的关键场景

### 场景 1：锁定超时与提交报名同时发生

- 定时任务扫描到 Lock 过期，准备置 `expired` 并 INCR；
- 用户恰好在此时提交报名。

**处理**：提交接口更新条件为
`WHERE token=? AND status='active' AND expires_at > NOW()`；
定时任务更新条件为 `WHERE id=? AND status='active'`。两条更新互斥，只有一方成功；
Redis 回补仅在定时任务成功时执行。提交失败返回 `LOCK_EXPIRED` / `LOCK_ALREADY_CONSUMED`，不创建 Enrollment。

### 场景 2：支付回调与支付超时同时发生

- 支付超时任务准备把 Order 置 `expired`；
- 微信回调同时到达。

**处理**：两者都对 Order 行加 `FOR UPDATE`，迁移条件均为 `status='pending_payment'`。
先获得锁的一方成功迁移，另一方 `affected_rows=0` 后跳过。
只有超时任务成功时执行 Redis INCR；支付回调成功时不执行 INCR（名额继续占用）。

### 场景 3：重复支付回调

微信可能重复发送通知。回调在事务内先查 Order 状态，非 `pending_payment` 直接 COMMIT 返回 200，
不执行任何 UPDATE，不碰 Redis。幂等键为微信 `transaction_id`，数据库唯一约束兜底。

### 场景 4：支付超时后迟到的支付成功回调

Order 已被置 `expired`，名额已回补。迟到的回调按场景 3 幂等返回 200，**不迁移、不回补**。
该笔支付必须在支付平台侧人工退款，并记录对账告警；禁止把 `expired` 改回 `paid`。

### 场景 5：后台调整名额与锁定并发

`PUT /admin/groups/{group_id}` 调整 `total_slots` 时：

1. 事务内锁定 `groups` 行，计算 `delta = new_total - old_total`；
2. 用单条 Lua 脚本原子执行 `new_remaining = remaining + delta`，
   若 `new_remaining < 0` 返回 `-1`，拒绝操作并返回 `GROUP_SOLD_OUT`；
3. 脚本成功后再提交 MySQL；MySQL 失败时按相反 delta 回补 Redis。

禁止“先读 remaining、再计算、再 SET”的三步写法，避免与并发锁定互相覆盖。

---

## 七、定时任务清单

| 任务 | 频率 | 扫描条件 | 单实例锁 | 幂等保障 |
|---|---|---|---|---|
| Lock 过期 | 每 15 秒 | `status='active' AND expires_at <= NOW()` | `job:lock-expiry` | 条件更新；`affected_rows=1` 才 INCR |
| 支付超时 | 每分钟 | `status='pending_payment' AND created_at <= NOW()-15min` | `job:pay-timeout` | 条件更新；`affected_rows=1` 才 INCR |
| 对账 | 每日 02:00；试报名阶段每日至少一次，可手动触发 | 全量 group | `job:reconcile` | 只读，不修改数据 |
| 报名自动确认 | 每 5 分钟 | `status='paid'` | `job:auto-confirm` | 条件更新 `WHERE status='paid'` |

所有任务必须使用 `SET job:{name}:lock NX PX 60000` 做单实例保护；
扫描必须使用 `FOR UPDATE SKIP LOCKED`。

---

## 八、对账等式

对每个 group，对账任务必须验证：

```text
Redis group:{group_id}:remaining
  + COUNT(enrollment_locks  WHERE group_id=? AND status='active')
  + COUNT(enrollments      WHERE group_id=? AND status='pending_payment')
  + COUNT(enrollments      WHERE group_id=? AND status IN ('paid','confirmed'))
  = groups.total_slots
```

- `pending_payment` 已消费锁但仍占名额，必须计入；
- `refunding` / `refund_failed` 不改变名额占用：Enrollment 仍为 `paid`/`confirmed`，
  Redis 不回补，等式仍按 Enrollment 计数；
- `cancelled` / `expired` / `refunded` 不计入，其名额已通过 INCR 回补；
- `refunded` 只在 `REFUND.SUCCESS` 时出现，Redis 已在同一事务中 `INCR` 恰好一次；
  `REFUND.CLOSED` / `REFUND.ABNORMAL` 不产生该状态；
- 等式任一项不成立即告警，输出：Redis 当前值、活跃 lock 数、待支付数、已支付/已确认数、
  MySQL `total_slots`，并附最近 24 小时相关状态迁移日志。

`GET /admin/stats/overview` 必须按同一口径实时返回上述四项，不得使用其它公式。

---

## 九、变更控制

本文件已于 **2026-10-25** 冻结。

任何状态迁移变更必须同时满足：

1. 经人工批准，并在变更说明中写明理由与影响范围；
2. 同步更新 `docs/openapi.yaml`（枚举、错误码、接口描述）；
3. 重新运行 `docs/design-verify/enumerate_state_machine.py --strict`，审计报告无警告；
4. 重新生成 `docs/design-verify/reports/state-machine-audit.{md,json}`；
5. 更新 `server/internal/contract` 契约测试骨架并同步归档副本。

未完成上述步骤的迁移一律视为非法，代码评审必须拒绝。
