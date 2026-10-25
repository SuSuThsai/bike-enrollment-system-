CREATE TABLE IF NOT EXISTS enrollment_locks (
    token       CHAR(36)    NOT NULL,
    user_id     CHAR(36)    NOT NULL,
    group_id    CHAR(36)    NOT NULL,
    status      VARCHAR(16) NOT NULL,
    expires_at  DATETIME(3) NOT NULL,
    consumed_at DATETIME(3) NULL,
    expired_at  DATETIME(3) NULL,
    created_at  DATETIME(3) NOT NULL,
    updated_at  DATETIME(3) NOT NULL,
    PRIMARY KEY (token),
    KEY idx_locks_user_group (user_id, group_id),
    KEY idx_locks_status_expires (status, expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
