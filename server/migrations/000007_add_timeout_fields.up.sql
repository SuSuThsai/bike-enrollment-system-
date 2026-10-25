ALTER TABLE `orders`
    ADD COLUMN expired_at DATETIME(3) NULL AFTER paid_at;

ALTER TABLE enrollments
    ADD COLUMN cancelled_at DATETIME(3) NULL AFTER paid_at;
