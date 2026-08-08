-- package: err

-- model: Item { id int64, name string }

-- param: id int64
-- name: DeleteReturn :one
-- model: Item
DELETE FROM items WHERE id = @id
RETURNING id, name
