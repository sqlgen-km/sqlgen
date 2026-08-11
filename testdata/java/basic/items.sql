-- package: com.example

-- model: Item { id int64, name string, price float64, stock int32 }

-- param: id int64
-- name: FindByID :one
-- model: Item
SELECT id, name, price, stock FROM items WHERE id = @id

-- name: FindAll :many
-- model: Item
SELECT id, name, price, stock FROM items ORDER BY id

-- name: CountItems :one
-- model int64
SELECT COUNT(*) FROM items

-- param: name string, price float64, stock int32
-- name: InsertItem :one
-- model int64
INSERT INTO items (name, price, stock) VALUES (@name, @price, @stock) RETURNING id

-- param: name string, price float64, id int64
-- name: UpdateItem :execrows
UPDATE items SET name = @name, price = @price WHERE id = @id

-- param: id int64
-- name: DeleteItem :exec
DELETE FROM items WHERE id = @id
