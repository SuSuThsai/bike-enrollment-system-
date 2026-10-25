-- KEYS[1] = group:{group_id}:remaining
-- ARGV[1] = total_slots 差值 delta
if redis.call('EXISTS', KEYS[1]) == 0 then
    return -2
end

local remaining = tonumber(redis.call('GET', KEYS[1]))
if not remaining then
    return -2
end

local new_remaining = remaining + tonumber(ARGV[1])
if new_remaining < 0 then
    return -1
end

redis.call('SET', KEYS[1], new_remaining)
return new_remaining
