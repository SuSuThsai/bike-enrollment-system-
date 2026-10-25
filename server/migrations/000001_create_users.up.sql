CREATE TABLE IF NOT EXISTS users (
    id         CHAR(36)     NOT NULL,
    openid     VARCHAR(64)  NOT NULL,
    unionid    VARCHAR(64)  NOT NULL DEFAULT '',
    nickname   VARCHAR(64)  NOT NULL DEFAULT '',
    avatar_url VARCHAR(512) NOT NULL DEFAULT '',
    role       VARCHAR(16)  NOT NULL DEFAULT 'rider',
    created_at DATETIME(3)  NOT NULL,
    updated_at DATETIME(3)  NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_users_openid (openid),
    KEY idx_users_unionid (unionid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
