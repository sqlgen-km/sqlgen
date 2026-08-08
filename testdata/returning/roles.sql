-- package: returning

-- model: Role { id int64, name string, created_at time.Time, updated_at time.Time }

-- @创建角色并返回完整信息
-- param: name string, created_at time.Time, updated_at time.Time
-- name: InsertRoleFull :execrows
INSERT INTO roles (name, created_at, updated_at)
VALUES (@name, @created_at, @updated_at)

-- @创建角色并返回ID
-- param: name string, created_at time.Time, updated_at time.Time
-- name: InsertRoleID :one
-- model int64
INSERT INTO roles (name, created_at, updated_at)
VALUES (@name, @created_at, @updated_at)
RETURNING id

-- @根据ID查询
-- param: id int64
-- name: FindByID :one
-- model: Role
SELECT id, name, created_at, updated_at
FROM roles
WHERE id = @id

-- @查询全部
-- name: FindAll :many
-- model: Role
SELECT id, name, created_at, updated_at
FROM roles
ORDER BY id

-- @更新角色并返回
-- param: name string, id int64
-- name: UpdateRole :exec
UPDATE roles SET name = @name, updated_at = NOW()
WHERE id = @id

-- @删除角色
-- param: id int64
-- name: DeleteRole :execrows
DELETE FROM roles WHERE id = @id

-- @删除角色并返回信息
-- param: id int64
-- name: DeleteRoleSafe :execrows
DELETE FROM roles WHERE id = @id
