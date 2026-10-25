-- KEYS[1] = group:{group_id}:remaining
-- KEYS[2] = lock:rollback:{lock_token}
-- ARGV[1] = 补偿幂等键 TTL（毫秒）
local ok = redis.call('SET', KEYS[2], '1', 'NX', 'PX', ARGV[1])
if not ok then
    return 0
end

redis.call('INCR', KEYS[1])
return 1
