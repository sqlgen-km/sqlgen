-- package: complex

-- model: UserWithOrders { user_id int64, display_name string, order_count int64 }

-- param: min_orders int64
-- name: ActiveUsers :many
-- model: UserWithOrders
SELECT u.id AS user_id, u.display_name, COUNT(o.id) AS order_count
FROM users u
LEFT JOIN orders o ON u.id = o.user_id
WHERE o.status = 'active'
GROUP BY u.id, u.display_name
HAVING COUNT(o.id) >= @min_orders

-- param: user_id int64
-- name: HasOrders :one
-- model { has_orders bool }
SELECT COUNT(*) > 0 AS has_orders FROM orders WHERE user_id = @user_id

-- param: name *string, limit int32
-- name: SearchByName :many
-- model: UserWithOrders
SELECT u.id AS user_id, u.display_name, COUNT(o.id) AS order_count
FROM users u
LEFT JOIN orders o ON u.id = o.user_id
WHERE (u.display_name LIKE @name OR @name IS NULL)
GROUP BY u.id, u.display_name
LIMIT @limit
