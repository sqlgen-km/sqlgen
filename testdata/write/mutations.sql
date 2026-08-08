-- package: write

-- model: User { id int64, display_name string, gender string, avatar string, created_at time.Time }

-- param: id int64, display_name string, gender string
-- name: UpdateUser :exec
UPDATE users SET display_name = @display_name, gender = @gender
WHERE id = @id

-- param: id int64
-- name: DeleteAndReturn :execrows
DELETE FROM users
WHERE id = @id
