-- KEYS[1] = group:{group_id}:remaining
-- ARGV[1] = 本次锁定预减的名额数（固定为 1）
local remaining = tonumber(redis.call('GET', KEYS[1]) or '0')
if remaining <= 0 then
    return -1
end

redis.call('DECR', KEYS[1])
return redis.call('GET', KEYS[1])
