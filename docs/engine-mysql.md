# MySQL 引擎实现方案

## 1. 占位符

```
DSL:    @id → ?, @name → ?
生成:   ?, ?, ?...
```

所有参数统一 `?`，无编号。

## 2. RETURNING

MySQL 无 RETURNING 语法。分情况处理。

### 2.1 INSERT RETURNING 单列（id）

拆成两条语句：先 INSERT，再 `SELECT LAST_INSERT_ID()`。

```sql
-- SQL 常量 1：INSERT
INSERT INTO roles (name) VALUES (?)

-- SQL 常量 2：SELECT LAST_INSERT_ID()
SELECT LAST_INSERT_ID()
```

Go 实现：

```go
func (q *queriesMySQL) InsertRoleID(ctx context.Context, name string) (int64, error) {
    stmt := q.insertRole
    if q.tx != nil { stmt = q.tx.Stmt(stmt) }
    _, err := stmt.ExecContext(ctx, name)
    if err != nil { return 0, err }
    row := q.lastID.QueryRowContext(ctx)
    var id int64
    if err := row.Scan(&id); err != nil { return 0, err }
    return id, nil
}
```

### 2.2 INSERT RETURNING 多列 / *

拆成：INSERT → `SELECT LAST_INSERT_ID()` → `SELECT cols FROM table WHERE id = ?`

```sql
-- SQL 常量 1：INSERT
INSERT INTO roles (name, created_at) VALUES (?, ?)

-- SQL 常量 2：SELECT 回查
SELECT id, name, created_at, updated_at FROM roles WHERE id = ?
```

Go 实现：

```go
func (q *queriesMySQL) InsertRoleFull(ctx context.Context, name string) (*Role, error) {
    stmt := q.insertRole
    if q.tx != nil { stmt = q.tx.Stmt(stmt) }
    result, err := stmt.ExecContext(ctx, name)
    if err != nil { return nil, err }
    id, _ := result.LastInsertId()
    row := q.selectRole.QueryRowContext(ctx, id)
    var r Role
    if err := row.Scan(&r.ID, &r.Name, &r.CreatedAt, &r.UpdatedAt); err != nil {
        return nil, err
    }
    return &r, nil
}
```

### 2.3 UPDATE RETURNING

需在事务内：`SELECT ... FOR UPDATE` → `UPDATE` → 返回 SELECT 结果。

```sql
-- SQL 常量 1：SELECT FOR UPDATE
SELECT id, name FROM roles WHERE id = ? FOR UPDATE

-- SQL 常量 2：UPDATE
UPDATE roles SET name = ? WHERE id = ?
```

Go 实现：

```go
func (q *queriesMySQL) UpdateRole(ctx context.Context, name string, id int64) (*Role, error) {
    // Step 1: SELECT FOR UPDATE
    stmt := q.selectForUpdate
    if q.tx != nil { stmt = q.tx.Stmt(stmt) }
    row := stmt.QueryRowContext(ctx, id)
    var r Role
    if err := row.Scan(&r.ID, &r.Name); err != nil { return nil, err }

    // Step 2: UPDATE
    upStmt := q.updateRole
    if q.tx != nil { upStmt = q.tx.Stmt(upStmt) }
    _, err := upStmt.ExecContext(ctx, name, id)
    if err != nil { return nil, err }

    return &r, nil
}
```

### 2.4 DELETE RETURNING

同 UPDATE：`SELECT ... FOR UPDATE` → `DELETE` → 返回 SELECT 结果。

## 3. ON CONFLICT

| DSL | 生成 |
|-----|------|
| `ON CONFLICT (c) DO NOTHING` | `INSERT IGNORE INTO ...` |
| `ON CONFLICT (c) DO UPDATE SET` | `ON DUPLICATE KEY UPDATE ...` |

```sql
-- DO NOTHING
INSERT IGNORE INTO groups_member (user_id, group_id) VALUES (?, ?)

-- DO UPDATE
INSERT INTO users_credential (user_id, credential_type, credential_value, salt)
VALUES (?, ?, ?, ?)
ON DUPLICATE KEY UPDATE credential_value = VALUES(credential_value), salt = VALUES(salt)
```

**执行方式**：`ExecContext`（无 RETURNING）/ `ExecContext` → `SELECT LAST_INSERT_ID()` → `SELECT`（有 RETURNING）

## 4. LIMIT / OFFSET

```sql
LIMIT @limit OFFSET @offset  →  LIMIT ? OFFSET ?
```

## 5. ILIKE

无原生 ILIKE，翻译为 LOWER + LIKE。

```sql
WHERE name ILIKE @pattern  →  WHERE LOWER(name) LIKE LOWER(?)
```

## 6. COALESCE

MySQL 用 `IFNULL`（两参数）或 `COALESCE`（多参数，推荐）。

```sql
COALESCE(@status, status)  →  IFNULL(?, status)
-- 多参数用 COALESCE（MySQL 5.7+ 同标准 SQL）
```

## 7. NOW()

```sql
NOW()  →  NOW()
CURRENT_DATE  →  CURDATE()
```

## 8. BOOLEAN

```sql
TRUE / FALSE  →  TRUE / FALSE  （或 1 / 0）
```

## 9. LIKE

```sql
WHERE name LIKE @pattern  →  WHERE name LIKE ?
```

## 10. CTE

```sql
WITH cte AS (...) SELECT ...  →  不变（MySQL 8.0+）
WITH RECURSIVE cte AS (...)  →  不变
```

**限制**：MySQL 8.0+，递归深度默认 1000，`cte_max_recursion_depth` 可调。

## 11. 字符串拼接

```sql
name || ' - ' || code  →  CONCAT(name, ' - ', code)
```

## 12. 当前时间

```sql
NOW()  →  NOW()
```

## 13. 数组

无原生数组。建议应用层处理或用 JSON 函数。

## 14. NULL 排序

无 NULLS FIRST/LAST 语法，用 IS NULL 表达式。

```sql
ORDER BY c NULLS FIRST  →  ORDER BY c IS NULL DESC, c
ORDER BY c NULLS LAST   →  ORDER BY c IS NULL ASC, c
```

## 15. 执行方式

| 操作 | Go 实现 |
|------|---------|
| SELECT :one | `PrepareContext → QueryRowContext → Scan` |
| SELECT :many | `PrepareContext → QueryContext → rows.Scan` |
| INSERT :exec | `PrepareContext → ExecContext` |
| INSERT :one (RETURNING) | `PrepareContext → ExecContext → lastID.QueryRowContext → Scan` |
| INSERT :one (RETURNING *) | `PrepareContext → ExecContext → selectBack.QueryRowContext → Scan` |
| INSERT ON CONFLICT | `PrepareContext → ExecContext` |
| UPDATE :exec | `PrepareContext → ExecContext` |
| UPDATE :one (RETURNING) | `PrepareContext → (selectForUpdate).QueryRowContext → Scan → ExecContext` |
| DELETE :execrows | `PrepareContext → ExecContext → RowsAffected` |
| DELETE :one (RETURNING) | `PrepareContext → (selectForUpdate).QueryRowContext → Scan → ExecContext` |

**关键差异**：
- RETURNING 需要多步执行（INSERT + SELECT / SELECT FOR UPDATE + DML）
- UPDATE/DELETE RETURNING 要求事务内执行
- queriesMySQL struct 需要额外字段：`lastID *sql.Stmt`、`selectBack *sql.Stmt`、`selectForUpdate *sql.Stmt`

## 16. 生成文件

```
roles.sql.mysql.go
  - SQL 常量（MySQL 方言）
  - queriesMySQL struct（需额外 stmt 字段）
  - NewMySQL 构造器（prepare 多条语句）
  - 方法体（多步执行逻辑）
```
