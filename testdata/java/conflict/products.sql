-- package: com.example

-- model: Product { sku string, name string, price float64, stock int32 }

-- param: sku string, name string, price float64
-- name: UpsertProduct :exec
INSERT INTO products (sku, name, price) VALUES (@sku, @name, @price)
ON CONFLICT (sku) DO UPDATE SET name = @name, price = @price

-- param: sku string, name string
-- name: UpsertIgnore :exec
INSERT INTO products (sku, name) VALUES (@sku, @name)
ON CONFLICT (sku) DO NOTHING
