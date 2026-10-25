ALTER TABLE enrollments
    DROP COLUMN cancelled_at;

ALTER TABLE `orders`
    DROP COLUMN expired_at;
