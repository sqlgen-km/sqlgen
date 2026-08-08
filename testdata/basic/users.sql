-- package: basic

-- model: User { id int64, display_name string, gender string, avatar string, created_at time.Time }
-- model: Filter { gender string }
-- model: InsertUserParams { display_name string, gender string, avatar string }
-- model: UserBrief { id int64, display_name string }

-- param: filter Filter, limit int32, offset int32
-- name: FindByGender :many
-- model: User
SELECT id, display_name, gender, avatar
FROM users
WHERE gender = @filter.gender AND deleted_at IS NULL
ORDER BY id DESC
LIMIT @limit OFFSET @offset

-- param: limit int32
-- name: ListBrief :many
-- model: UserBrief
SELECT id, display_name, gender, avatar, created_at
FROM users
ORDER BY id DESC
LIMIT @limit

-- param: arg InsertUserParams
-- name: InsertUser :one
-- model int64
INSERT INTO users (display_name, gender, avatar)
VALUES (@arg.display_name, @arg.gender, @arg.avatar)
RETURNING id

-- param: id int64
-- name: DeleteUser :exec
DELETE FROM users WHERE id = @id

-- param: id int64
-- name: GetUserMapped :one
-- model: User={id:user_id,display_name:name,gender,avatar,created_at:created}
SELECT id AS user_id, display_name AS name, gender, avatar, created_at AS created
FROM users
WHERE id = @id
