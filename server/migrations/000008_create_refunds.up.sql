CREATE TABLE IF NOT EXISTS refunds (
    id            CHAR(36)    NOT NULL,
    order_id      CHAR(36)    NOT NULL,
    out_refund_no VARCHAR(64) NOT NULL,
    refund_id     VARCHAR(64) NULL,
    amount        INT         NOT NULL,
    status        VARCHAR(16) NOT NULL,
    created_at    DATETIME(3) NOT NULL,
    updated_at    DATETIME(3) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_refunds_out_refund_no (out_refund_no),
    UNIQUE KEY uk_refunds_refund_id (refund_id),
    KEY idx_refunds_order (order_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
