-- package: err

-- model: Item { id int64, name string }

-- param: name string
-- name: InsertStar :one
-- model: Item
INSERT INTO items (name) VALUES (@name)
RETURNING *
