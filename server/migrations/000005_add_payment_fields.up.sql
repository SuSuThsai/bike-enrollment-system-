ALTER TABLE enrollments
    ADD COLUMN paid_at DATETIME(3) NULL AFTER updated_at;

ALTER TABLE `orders`
    ADD COLUMN transaction_id VARCHAR(64) NULL AFTER status,
    ADD UNIQUE KEY uk_orders_transaction_id (transaction_id);
