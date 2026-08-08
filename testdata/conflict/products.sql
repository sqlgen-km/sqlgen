-- package: conflict

-- model: Product { id int64, sku string, name string, price float64, stock int32 }

-- param: sku string, name string, price float64
-- name: UpsertProduct :exec
INSERT INTO products (sku, name, price, stock)
VALUES (@sku, @name, @price, 1)
ON CONFLICT (sku) DO UPDATE SET name = @name, price = @price

-- param: sku string, name string, price float64
-- name: UpsertProductRC :one
-- model int64
INSERT INTO products (sku, name, price, stock)
VALUES (@sku, @name, @price, 1)
ON CONFLICT (sku) DO NOTHING
RETURNING id
