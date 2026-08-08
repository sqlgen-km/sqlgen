-- package: err

-- model: Item { id int64, name string, category string }

-- param: name string, category string
-- name: InsertMulti :one
-- model: Item
INSERT INTO items (name, category) VALUES (@name, @category)
RETURNING id, name, category
