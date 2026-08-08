-- package: multi

-- model: User { id int64, display_name string }

-- param: id int64
-- name: FindByID :one
-- model: User
SELECT id, display_name FROM users WHERE id = @id
