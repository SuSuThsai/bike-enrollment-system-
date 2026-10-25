CREATE TABLE IF NOT EXISTS events (
    id          CHAR(36)     NOT NULL,
    title       VARCHAR(200) NOT NULL,
    cover_url   VARCHAR(512) NOT NULL DEFAULT '',
    start_time  DATETIME(3)  NOT NULL,
    location    VARCHAR(255) NOT NULL,
    status      VARCHAR(32)  NOT NULL,
    description TEXT         NOT NULL,
    rules       TEXT         NOT NULL,
    created_at  DATETIME(3)  NOT NULL,
    updated_at  DATETIME(3)  NOT NULL,
    PRIMARY KEY (id),
    KEY idx_events_status_start (status, start_time)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `groups` (
    id                 CHAR(36)     NOT NULL,
    event_id           CHAR(36)     NOT NULL,
    name               VARCHAR(64)  NOT NULL,
    price              INT          NOT NULL,
    total_slots        INT          NOT NULL,
    start_time         DATETIME(3)  NOT NULL,
    status             VARCHAR(16)  NOT NULL DEFAULT 'open',
    form_schema        JSON         NOT NULL,
    disclaimer_version VARCHAR(64)  NOT NULL,
    created_at         DATETIME(3)  NOT NULL,
    updated_at         DATETIME(3)  NOT NULL,
    PRIMARY KEY (id),
    KEY idx_groups_event_start (event_id, start_time)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
