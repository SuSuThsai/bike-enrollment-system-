# 设计验证归档

本目录归档 2026-10-25 设计冻结时的两类验证资产：

1. **状态机穷举脚本**：解析 [`docs/state-machine.md`](../state-machine.md) 的迁移表，
   枚举全部「状态 × 事件」组合，检查终态、死锁与可达性；
2. **契约测试骨架**：基于 [`docs/openapi.yaml`](../openapi.yaml) 的 HTTP 契约测试快照，
   校验合法请求、必填字段、成功响应结构与错误码枚举。

冻结声明与变更流程见 [`docs/state-machine.md`](../state-machine.md) 开篇及第九节。
本目录只做验证，不包含业务实现。

## 目录结构

```text
docs/design-verify/
├── README.md                     # 本文件
├── enumerate_state_machine.py    # 状态机穷举/审计脚本（唯一数据源是 state-machine.md）
├── reports/
│   ├── state-machine-audit.md    # 脚本生成的审计报告
│   └── state-machine-audit.json  # 机器可读审计结果
└── contract/                     # 契约测试骨架快照（可执行权威版本在 server）
    ├── README.md
    ├── contract_test.go          # 路径表 + 四项检查主流程
    ├── openapi_test.go           # OpenAPI 解析、$ref/allOf 展开、样例生成
    ├── spec_quality_test.go      # 契约自身质量检查
    └── *_test.go                 # 按 tag 拆分的入口
```

## 一、状态机穷举

```bash
# 在仓库根目录运行；--strict 让“警告”级发现以退出码 1 结束
python docs/design-verify/enumerate_state_machine.py --strict

# 重新生成归档报告
python docs/design-verify/enumerate_state_machine.py \
  --md docs/design-verify/reports/state-machine-audit.md \
  --json docs/design-verify/reports/state-machine-audit.json \
  --strict
```

脚本不硬编码状态或事件，全部从文档迁移表解析；文档改动后结论自动跟随。

### 当前结论（完整口径，退款已纳入）

| 项目 | 结果 |
|---|---|
| 状态机 | 3 台（Lock / Order / Enrollment） |
| 状态 | 17 个（含 3 个初始伪状态「（无）」） |
| 事件 | 14 个 |
| (状态 × 事件) 组合 | 84 个 |
| 合法迁移 | 15 条 |
| 应被拒绝 | 69 条（R1 终态 34、R2 未定义 24、R3 实体未创建 11） |
| 死锁状态 | 无 |
| 孤立状态 | 无 |
| 文档告警 | **0 警告**；0 条提示 |

完整明细见 [`reports/state-machine-audit.md`](reports/state-machine-audit.md)。

## 二、契约测试骨架

可执行版本位于 `server/internal/contract`，归档快照位于 [`contract/`](contract/)。

```bash
cd server

# 契约骨架（处理器仍为 501 时，相关断言以 skip 保留）
go test ./internal/contract -v

# 契约自身质量：operationId 唯一、必填输入声明 400、$ref 可解析、4xx/5xx 统一错误信封
go test ./internal/contract -run 'TestOperationIDs|TestInputOperations|TestAllLocalRefs|TestErrorResponses' -v

# 强制契约断言立即生效（接口仍返回 501 时失败）
CONTRACT_STRICT=1 go test ./internal/contract
```

骨架只做四类业务检查：

1. 合法请求是否被接受（按 schema 生成请求，断言 2xx）；
2. 缺失必填字段是否被拒绝（断言 400/422 且错误体完整）；
3. 成功响应是否包含 schema 声明的全部 `required` 字段（含 `allOf` / `$ref` / 数组 / 嵌套对象）；
4. 错误响应的 `code` 是否落在 `ErrorResponse.code` 枚举内。

另有 `spec_quality_test.go` 检查契约自身的完整性，不依赖业务实现。

### 覆盖范围

OpenAPI 中 17 个操作全部有测试入口，`TestContractCoverage` 双向校验：

| Tag | 操作 |
|---|---|
| auth | `POST /auth/wechat-login` |
| events | `GET /events`、`GET /events/{event_id}` |
| enrollments | `POST /enrollments/lock`、`POST /enrollments`、`GET /enrollments/my`、`GET /enrollments/{enrollment_id}` |
| payments | `POST /payments/prepay`、`POST /payments/notify` |
| notifications | `POST /notifications/subscribe` |
| admin | `POST /admin/events`、`PUT /admin/events/{event_id}`、`POST /admin/groups`、`PUT /admin/groups/{group_id}`、`GET /admin/enrollments`、`GET /admin/enrollments/export`、`GET /admin/stats/overview` |

## 三、冻结与变更

本目录与 `docs/openapi.yaml`、`docs/state-machine.md` 同批冻结。任何状态迁移或契约变更必须：

1. 经人工批准；
2. 同步更新 `docs/state-machine.md` 与 `docs/openapi.yaml`；
3. 重新运行穷举脚本并保持 `--strict` 退出码为 0；
4. 重新生成 `reports/state-machine-audit.{md,json}`；
5. 更新 `server/internal/contract`，并把 `*_test.go` 重新复制到 `contract/` 快照。

## 四、已知边界

- **OpenAPI 接口已全部实现**：契约测试的合法请求、错误码与 required 字段断言默认生效；
  `CONTRACT_STRICT=1` 仍保留，用于把任何残留的 501 占位 skip 升级为失败。
- **支付回调**：契约已定义四个 `Wechatpay-*` 请求头与回调信封；契约测试因没有微信平台私钥，
  合法回调用例暂时 skip；真实 provider 的 HTTP 回调由 `server/tests/integration/wechat_notify_test.go` 覆盖。
- **退款状态枚举**：`spec_quality_test.go` 新增 Order/Enrollment 状态枚举与退款状态机一致性检查。
- **导出接口**返回 xlsx 二进制，没有 JSON `required` 字段，第 3 项检查 skip。
- **事件列表**只声明 500，没有客户端可触发的 4xx，错误码检查 skip。
- 契约快照用于阅读与评审；Go 测试依赖 `server` 模块内部包，不能直接在 `docs/` 下运行。
