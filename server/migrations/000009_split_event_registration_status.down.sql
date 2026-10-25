ALTER TABLE events RENAME INDEX idx_events_lifecycle_start TO idx_events_status_start;

-- 回滚：registration_open / registration_closed 合并后的 published 统一还原为 registration_open
UPDATE events
   SET lifecycle_status = CASE
        WHEN lifecycle_status = 'finished' THEN 'finished'
        WHEN lifecycle_status = 'cancelled' THEN 'cancelled'
        WHEN lifecycle_status = 'draft' THEN 'draft'
        ELSE 'registration_open'
   END;

ALTER TABLE events
    DROP COLUMN registration_closed_reason,
    DROP COLUMN registration_manually_closed,
    DROP COLUMN registration_deadline,
    DROP COLUMN registration_open_at;

ALTER TABLE events
    CHANGE COLUMN lifecycle_status status VARCHAR(32) NOT NULL;
