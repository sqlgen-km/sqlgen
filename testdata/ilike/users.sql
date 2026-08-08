-- package: ilike_search

-- model: User { id int64, name string, email string }

-- param: name string
-- name: SearchByName :many
-- model: User
SELECT id, name, email
FROM users
WHERE name ILIKE @name
ORDER BY id

-- param: name string, email string
-- name: SearchByBoth :many
-- model: User
SELECT id, name, email
FROM users
WHERE name ILIKE @name OR email ILIKE @email
ORDER BY id
