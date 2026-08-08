-- package: multi

-- param: display_name string, gender string
-- name: CreateUser :one
-- model int64
INSERT INTO users (display_name, gender)
VALUES (@display_name, @gender)
RETURNING id
