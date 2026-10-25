ALTER TABLE `orders`
    DROP INDEX uk_orders_transaction_id,
    DROP COLUMN transaction_id;

ALTER TABLE enrollments
    DROP COLUMN paid_at;
