CREATE TABLE IF NOT EXISTS notification_authorizations (
    id          CHAR(36)    NOT NULL,
    user_id     CHAR(36)    NOT NULL,
    template_id VARCHAR(64) NOT NULL,
    status      VARCHAR(16) NOT NULL DEFAULT 'available',
    created_at  DATETIME(3) NOT NULL,
    consumed_at DATETIME(3) NULL,
    PRIMARY KEY (id),
    KEY idx_notification_auth_lookup (user_id, template_id, status, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
