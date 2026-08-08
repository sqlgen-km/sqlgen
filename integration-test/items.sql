-- package: integration

-- model: Item { id int64, name string, category string, price float64, stock int32, is_active bool, created_at time.Time, updated_at time.Time }
-- model: ItemBrief { id int64, name string, category string }
-- model: CatCount { category string, cnt int64 }

-- ============================================================
-- SELECT :one (单行查询)
-- ============================================================
-- @根据ID查询单条记录
-- param: id int64
-- name: FindByID :one
-- model: Item
SELECT id, name, category, price, stock, is_active, created_at, updated_at
FROM items WHERE id = @id

-- ============================================================
-- SELECT :many (多行查询)
-- ============================================================
-- @查询全部记录
-- name: FindAll :many
-- model: Item
SELECT id, name, category, price, stock, is_active, created_at, updated_at
FROM items ORDER BY id

-- @按分类查询
-- param: category string
-- name: FindByCategory :many
-- model: Item
SELECT id, name, category, price, stock, is_active, created_at, updated_at
FROM items WHERE category = @category ORDER BY id

-- ============================================================
-- SELECT :many with LIMIT/OFFSET
-- ============================================================
-- @分页查询
-- param: page_limit int32, page_offset int32
-- name: ListPaged :many
-- model: Item
SELECT id, name, category, price, stock, is_active, created_at, updated_at
FROM items ORDER BY id LIMIT @page_limit OFFSET @page_offset

-- ============================================================
-- INSERT :exec (无返回)
-- ============================================================
-- @插入记录无返回
-- param: name string, category string, price float64, stock int32
-- name: InsertItem :exec
INSERT INTO items (name, category, price, stock)
VALUES (@name, @category, @price, @stock)

-- ============================================================
-- INSERT :execrows (返回影响行数)
-- ============================================================
-- @插入记录返回影响行数
-- param: name string, category string, price float64, stock int32
-- name: InsertItemRows :execrows
INSERT INTO items (name, category, price, stock)
VALUES (@name, @category, @price, @stock)

-- ============================================================
-- INSERT RETURNING id (:one scalar)
-- ============================================================
-- @插入并返回ID
-- param: name string, category string, price float64, stock int32
-- name: InsertAndReturnID :one
-- model int64
INSERT INTO items (name, category, price, stock)
VALUES (@name, @category, @price, @stock)
RETURNING id

-- ============================================================
-- UPDATE :exec (无返回)
-- ============================================================
-- @更新记录
-- param: name string, price float64, id int64
-- name: UpdateItem :exec
UPDATE items SET name = @name, price = @price, updated_at = NOW()
WHERE id = @id

-- ============================================================
-- DELETE :exec
-- ============================================================
-- @删除记录
-- param: id int64
-- name: DeleteItem :exec
DELETE FROM items WHERE id = @id

-- ============================================================
-- DELETE :execrows
-- ============================================================
-- @删除记录返回影响行数
-- param: id int64
-- name: DeleteItemRows :execrows
DELETE FROM items WHERE id = @id

-- ============================================================
-- ON CONFLICT DO UPDATE (upsert)
-- ============================================================
-- @冲突时更新
-- param: name string, category string, price float64, stock int32
-- name: UpsertItem :exec
INSERT INTO items (name, category, price, stock)
VALUES (@name, @category, @price, @stock)
ON CONFLICT (name) DO UPDATE SET category = @category, price = @price, stock = @stock

-- ============================================================
-- ON CONFLICT DO NOTHING
-- ============================================================
-- @冲突时忽略 (仅 MySQL INSERT IGNORE / Oracle MERGE)
-- param: name string, category string
-- name: UpsertIgnore :exec
INSERT INTO items (name, category, price, stock)
VALUES (@name, @category, 0, 0)
ON CONFLICT (name) DO NOTHING

-- ============================================================
-- ILIKE 模糊搜索 (Oracle/MySQL/MSSQL → LOWER LIKE LOWER)
-- ============================================================
-- @模糊搜索
-- param: keyword string
-- name: SearchByName :many
-- model: Item
SELECT id, name, category, price, stock, is_active, created_at, updated_at
FROM items WHERE name ILIKE @keyword ORDER BY id

-- ============================================================
-- ILIKE + IS NULL 可选过滤
-- ============================================================
-- @可选过滤搜索
-- param: keyword *string
-- name: SearchOptional :many
-- model: Item
SELECT id, name, category, price, stock, is_active, created_at, updated_at
FROM items
WHERE (name ILIKE @keyword OR @keyword IS NULL)
ORDER BY id

-- ============================================================
-- JOIN + GROUP BY + HAVING
-- ============================================================
-- @分类统计
-- name: CountByCategory :many
-- model: CatCount
SELECT category, COUNT(*) AS cnt
FROM items
GROUP BY category
HAVING COUNT(*) >= 0
ORDER BY category

-- ============================================================
-- EXISTS 子查询
-- ============================================================
-- @检查是否存在
-- param: name string
-- name: ExistsByName :one
-- model { exists_ bool }
SELECT EXISTS(SELECT 1 FROM items WHERE name = @name) AS exists_

-- ============================================================
-- NULL 参数处理
-- ============================================================
-- @多条件可选查询
-- param: category *string, min_price *float64, is_active *bool
-- name: FindByFilters :many
-- model: Item
SELECT id, name, category, price, stock, is_active, created_at, updated_at
FROM items
WHERE (category = @category OR @category IS NULL)
  AND (price >= @min_price OR @min_price IS NULL)
  AND (is_active = @is_active OR @is_active IS NULL)
ORDER BY id

-- ============================================================
-- Scalar COUNT
-- ============================================================
-- @计数
-- name: CountAll :one
-- model int64
SELECT COUNT(*) FROM items

-- ============================================================
-- Scalar string list
-- ============================================================
-- @获取所有名称
-- param: category string
-- name: ListNames :many
-- model string
SELECT name FROM items WHERE category = @category ORDER BY name

-- ============================================================
-- Brief projection
-- ============================================================
-- @简要查询
-- param: category string
-- name: ListBrief :many
-- model: ItemBrief
SELECT id, name, category FROM items WHERE category = @category ORDER BY id

-- ============================================================
-- SELECT with extra columns (should discard)
-- ============================================================
-- @查询多列，丢弃多余列
-- param: id int64
-- name: FindWithExtra :one
-- model: ItemBrief
SELECT id, name, category, price, stock, created_at FROM items WHERE id = @id
