-- 重建组别名额计数器（仅管理后台修复流程使用）。
--
-- 语义：以 MySQL 侧按对账等式反推出的 expected 为准，把 Redis 计数写回去。
-- 默认只允许在 key 缺失时重建，避免覆盖仍然可信的实时计数；
-- force='1' 时才允许覆盖已存在但取值不一致的 key（供漂移修复，需先停流量）。
--
-- KEYS[1] = group:{group_id}:remaining
-- ARGV[1] = expected（整数，按 remaining = total_slots - active - pending - paid/confirmed 反推）
-- ARGV[2] = force（'1' 允许覆盖）
--
-- 返回 {mode, previous}：
--    1 = created      原本缺失，已写入
--    2 = overwritten  已存在且 force='1'，已覆盖
--    3 = unchanged    已存在且与 expected 一致，无需写入
--   -1 = conflict     已存在且与 expected 不一致，且未开启 force
local current = redis.call('GET', KEYS[1])
if not current then
    redis.call('SET', KEYS[1], ARGV[1])
    return { 1, '' }
end

if tonumber(current) == tonumber(ARGV[1]) then
    return { 3, current }
end

if ARGV[2] ~= '1' then
    return { -1, current }
end

redis.call('SET', KEYS[1], ARGV[1])
return { 2, current }
