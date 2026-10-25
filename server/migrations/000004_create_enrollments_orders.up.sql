CREATE TABLE IF NOT EXISTS enrollments (
    id                    CHAR(36)     NOT NULL,
    user_id               CHAR(36)     NOT NULL,
    event_id              CHAR(36)     NOT NULL,
    group_id              CHAR(36)     NOT NULL,
    order_id              CHAR(36)     NOT NULL,
    status                VARCHAR(16)  NOT NULL,
    form_data             JSON         NOT NULL,
    created_at            DATETIME(3)  NOT NULL,
    updated_at            DATETIME(3)  NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_enrollments_user_group (user_id, group_id),
    KEY idx_enrollments_user_created (user_id, created_at),
    KEY idx_enrollments_group_status (group_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `orders` (
    id            CHAR(36)    NOT NULL,
    enrollment_id CHAR(36)    NOT NULL,
    user_id       CHAR(36)    NOT NULL,
    group_id      CHAR(36)    NOT NULL,
    amount        INT         NOT NULL,
    status        VARCHAR(16) NOT NULL,
    paid_at       DATETIME(3) NULL,
    created_at    DATETIME(3) NOT NULL,
    updated_at    DATETIME(3) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_orders_enrollment (enrollment_id),
    KEY idx_orders_status_created (status, created_at),
    KEY idx_orders_user_created (user_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS disclaimer_signatures (
    id                 CHAR(36)     NOT NULL,
    user_id            CHAR(36)     NOT NULL,
    enrollment_id      CHAR(36)     NOT NULL,
    group_id           CHAR(36)     NOT NULL,
    disclaimer_version VARCHAR(64)  NOT NULL,
    signed_at          DATETIME(3)  NOT NULL,
    client_ip          VARCHAR(45)  NOT NULL,
    user_agent         VARCHAR(512) NOT NULL,
    created_at         DATETIME(3)  NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_disclaimer_enrollment (enrollment_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
