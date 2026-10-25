// Package lua 存放通过 go:embed 加载的 Lua 脚本。
package lua

import _ "embed"

// LockSlotScript 是名额预减脚本，禁止在 Go 代码中拼接 Lua 字符串。
//
//go:embed lock.lua
var LockSlotScript string

// RollbackSlotScript 是幂等回补脚本：同一 lock_token 只允许 INCR 一次。
//
//go:embed rollback.lua
var RollbackSlotScript string

// JobUnlockScript 安全释放定时任务单实例锁。
//
//go:embed job_unlock.lua
var JobUnlockScript string

// GroupSlotsScript 原子调整组别名额并拒绝负剩余。
//
//go:embed group_slots.lua
var GroupSlotsScript string

// GroupRestoreScript 按对账等式重建组别名额计数器，默认只补缺失的 key。
//
//go:embed group_restore.lua
var GroupRestoreScript string
