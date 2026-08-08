-- package: exists_query

-- model: User { id int64, name string }
-- model: HasOrders { has_orders bool }

-- param: user_id int64
-- name: HasOrders :one
-- model: HasOrders
SELECT EXISTS (
    SELECT 1 FROM orders o WHERE o.user_id = @user_id AND o.status = 'active'
) AS has_orders

-- param: name string
-- name: FindWithOrders :many
-- model: User
SELECT u.id, u.name
FROM users u
WHERE EXISTS (
    SELECT 1 FROM orders o WHERE o.user_id = u.id AND o.status = 'active'
)
AND u.name ILIKE @name
