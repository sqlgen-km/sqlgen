-- package: err

-- model: User { name string, email string }

-- param: id int64
-- name: FindUser :one
-- model: User
SELECT id, display_name FROM users WHERE id = @id
