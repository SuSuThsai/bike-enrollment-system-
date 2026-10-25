# 不可协商的约束清单

以下约束优先级高于任何“更简洁”“更优雅”的实现方案。
AI 生成代码若违反任一条，一律视为缺陷，必须重写。

## 一、名额一致性

1. **Redis 名额扣减必须使用 Lua 脚本**，将“判断 remaining > 0”与“DECR”合并为一次原子执行。
   禁止任何形式的“先 GET 判断、再 DECR”两步写法，即使中间加锁也不允许。
2. **同一用户对同一组别的锁定必须串行化**，使用 `SET lock:{user_id}:{group_id} NX PX 15000`。
   已存在锁时直接返回 `RATE_LIMITED`，不进入 Lua 扣减。
3. **剩余名额的实时值只认 Redis**。`GET /events/:id` 返回的 `remaining_slots` 必须从 Redis 读取，
   禁止从 MySQL 实时 `COUNT`。
4. **组别名额变更必须同步 Redis**。后台修改 `total_slots` 时，新剩余 = 原剩余 + 差值；
   若新剩余 < 0，拒绝操作并返回 `GROUP_SOLD_OUT`。
5. **Redis 必须开启 AOF**（`appendonly yes`），且 `appendfsync everysec`。
   部署检查项，不满足不得上线。

## 二、支付回调

6. **先验签，后处理**。验签失败返回 401，不修改任何数据。
7. **先查状态，后迁移**。回调处理必须在事务内 `SELECT ... FOR UPDATE` 锁定 Order 行，
   仅当 `status='pending_payment'` 时才迁移至 `paid`。
8. **重复回调幂等**。重复回调直接 COMMIT 返回 200，不执行 UPDATE，不碰 Redis。
9. **金额校验**。回调金额与 Order.amount 不一致时，不迁移状态，记录告警日志，仍返回 200。
10. **事务边界**。Order 状态迁移与 Enrollment 状态迁移必须在同一数据库事务内，
    不允许分两次独立更新。
11. **支付回调不扣减 Redis**。名额已在 lock 阶段扣减，回调成功不执行 INCR/DECR。

## 三、超时释放

12. **释放前必须校验当前状态**。lock 过期任务更新条件为
    `WHERE status='active'`；支付超时任务更新条件为 `WHERE status='pending_payment'`。
    仅当 `affected_rows=1` 时才执行 Redis INCR 回补。
13. **锁与状态迁移原子**。定时任务对目标行使用 `FOR UPDATE SKIP LOCKED`，
    避免多实例并发扫描同一批记录。

## 四、订阅消息

14. **通知发送走异步旁路**。任何通知（支付结果、报名结果、赛前提醒）的发送
    不得在报名或支付事务内同步执行，必须投递到异步队列或 goroutine，
    发送失败仅记录日志，不影响主链路返回。
15. **模板授权消耗记录**。`POST /notifications/subscribe` 须记录用户授权了哪些模板，
    发送前检查该模板是否仍有未消耗的授权；无授权时降级为短信或跳过。

## 五、错误处理与日志

16. **禁止吞异常**。所有 `err != nil` 必须至少记录日志，业务错误须转换为
    openapi.yaml 中定义的 `code` 返回，不得返回裸 500。
17. **敏感信息脱敏**。日志中不得出现身份证号、手机号、OpenID 的完整值，
    身份证保留前 6 后 4，手机号保留前 3 后 4。
18. **请求 ID 贯穿**。每个请求生成 `request_id`，写入响应体与所有日志行，
    便于跨服务排查。

## 六、安全与合规

19. **身份证、体检报告等敏感字段加密存储**（AES-256-GCM，密钥来自环境变量，
    不得硬编码）。列表查询接口默认脱敏返回。
20. **免责协议签署记录不可篡改**。签署时记录 `disclaimer_version`、`signed_at`、
    `client_ip`、`user_agent`，写入独立表，不允许 UPDATE，只允许 INSERT。
21. **管理后台接口必须校验 role=admin**，且导出接口须记录操作日志
    （谁、何时、导出了哪场赛事、多少条记录）。

## 七、代码组织

22. **handler 层不直接操作 Redis 和 MySQL**。所有数据访问经由 `service` → `repository`。
23. **Lua 脚本集中存放**在 `server/internal/pkg/lua/` 下，以 `.lua` 文件形式管理，
    通过 `go:embed` 加载，禁止在 Go 代码中拼接 Lua 字符串。
24. **定时任务必须有单实例保护**。多实例部署时使用 Redis `SET job:{name}:lock NX PX 60000`
    确保同一时刻只有一个实例执行扫描。

## 八、测试要求

25. **以下三处必须有集成测试**，且在本地小并发下验证通过：
    - 并发 lock 同一组别，已支付 + 活跃锁定数 ≤ total_slots；
    - 重复支付回调，Order 仅迁移一次，Redis 计数不变；
    - 支付超时任务与支付回调竞争，最终状态一致且 Redis 回补恰好一次。
26. **对账脚本**须在试报名阶段每天运行一次，输出不一致项。