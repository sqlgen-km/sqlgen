-- package: inline_model

-- param: id int64
-- name: GetUserByID :one
-- model { id int64, display_name string, gender string }
SELECT id, display_name, gender
FROM users
WHERE id = @id

-- param: filter Filter, limit int32
-- name: SearchUsers :many
-- model: User
SELECT id, display_name, gender
FROM users
WHERE gender = @filter.gender
LIMIT @limit

-- model: Filter { gender string }
-- model: User { id int64, display_name string, gender string }
