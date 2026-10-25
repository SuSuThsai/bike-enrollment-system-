# 里程碑二设计约束

## 范围
- 仅设计个人报名 MVP，禁止出现 Team、TeamMember、refund 相关字段或接口。
- 迭代二预留的能力，必须在 OpenAPI 中用 description 标注“迭代二”，但不得定义具体 schema。

## 错误码
- 每个接口的每个非 2xx 分支，必须映射到 ErrorResponse.code 的枚举值。
- 禁止使用 "error": "something went wrong" 这类自由字符串。

## 状态机
- 先输出状态机表格，表格通过人工审查后，才允许生成 OpenAPI 路径。
- 状态机必须覆盖：Lock（active/consumed/expired）、Order（pending_payment/paid/expired/refunded）、Enrollment（pending_payment/paid/confirmed/cancelled/refunded）。
- 终态必须显式标注“禁止任何出边”。

## 字段约定
- 金额一律 integer 分，字段名以 amount / price 结尾。
- 时间一律 RFC3339 字符串。
- 剩余名额字段名固定为 remaining_slots，来源标注为 “Redis”。