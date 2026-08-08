-- package: err

-- model: Item { id int64, name string }

-- param: name string, id int64
-- name: UpdateReturn :one
-- model: Item
UPDATE items SET name = @name WHERE id = @id
RETURNING id, name
