-- package: params

-- model: User { id int64, display_name string }

-- param: name *string, gender string, age *int64, active bool
-- name: FindUsers :many
-- model: User
SELECT id, display_name
FROM users
WHERE (name = @name OR @name IS NULL) AND gender = @gender AND (age = @age OR @age IS NULL) AND active = @active

-- param: id int64
-- name: DeleteUser :exec
DELETE FROM users WHERE id = @id

-- param:
-- name: CountAll :one
-- model { total int64 }
SELECT COUNT(*) AS total FROM users
