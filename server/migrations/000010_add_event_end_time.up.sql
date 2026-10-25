-- 赛事增加结束时间，并为历史数据补齐时间窗口：
--   registration_open_at < registration_deadline <= start_time < end_time
ALTER TABLE events ADD COLUMN end_time DATETIME(3) NULL AFTER start_time;

UPDATE events
   SET end_time = DATE_ADD(start_time, INTERVAL 2 HOUR)
 WHERE end_time IS NULL;

-- 历史赛事补齐报名窗口（仅填充 NULL；已有值保持不变）
UPDATE events
   SET registration_open_at = DATE_SUB(start_time, INTERVAL 7 DAY)
 WHERE registration_open_at IS NULL;

UPDATE events
   SET registration_deadline = DATE_SUB(start_time, INTERVAL 1 DAY)
 WHERE registration_deadline IS NULL;

ALTER TABLE events MODIFY COLUMN end_time DATETIME(3) NOT NULL;
