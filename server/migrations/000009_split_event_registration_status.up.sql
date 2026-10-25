-- 拆分赛事生命周期与报名状态：
--   status -> lifecycle_status（draft/published/finished/cancelled）
--   新增报名窗口、手动关闭标记与关闭原因
--   registration_open_at / registration_deadline 为 NULL 表示该侧不限（兼容历史数据：保持“报名开放”）
ALTER TABLE events
    CHANGE COLUMN status lifecycle_status VARCHAR(32) NOT NULL DEFAULT 'draft';

ALTER TABLE events
    ADD COLUMN registration_open_at DATETIME(3) NULL AFTER lifecycle_status,
    ADD COLUMN registration_deadline DATETIME(3) NULL AFTER registration_open_at,
    ADD COLUMN registration_manually_closed TINYINT(1) NOT NULL DEFAULT 0 AFTER registration_deadline,
    ADD COLUMN registration_closed_reason VARCHAR(255) NULL AFTER registration_manually_closed;

-- 旧的报名状态归并到生命周期：registration_open / registration_closed 都视为 published
UPDATE events
   SET lifecycle_status = CASE
        WHEN lifecycle_status IN ('registration_open', 'registration_closed') THEN 'published'
        WHEN lifecycle_status IN ('draft', 'published', 'finished', 'cancelled') THEN lifecycle_status
        ELSE 'published'
   END;

ALTER TABLE events RENAME INDEX idx_events_status_start TO idx_events_lifecycle_start;
