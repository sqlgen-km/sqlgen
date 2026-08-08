-- package: scalar

-- model: User { id int64, display_name string }

-- param: user_id int64
-- name: CountOrders :one
-- model int64
SELECT COUNT(*) FROM orders WHERE user_id = @user_id

-- param: gender string
-- name: ListNames :many
-- model string
SELECT display_name FROM users WHERE gender = @gender

-- param: id int64
-- name: FindUser :one
-- model: User
SELECT id, display_name FROM users WHERE id = @id
