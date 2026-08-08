# sqlgen DSL 规范 — Querier 模式映射

## 1. 指令

| 指令 | 语法 | 说明 |
|------|------|------|
| `-- package: pkg` | 文件首行 | 声明 Go 包名 |
| `-- model: Name { fields }` | 任意位置 | 声明结构体，可带字段或引用已有 |
| `-- model: Name` | 无字段 | 引用已声明的 model 作为返回类型 |
| `-- model int64` | 标量 | 返回基础类型 |
| `-- param: name Type, ...` | `-- name` 之前 | 声明方法入参 |
| `-- name: Method :mode` | SQL 之前 | 声明方法名和模式 |

## 2. 模式 → Querier 签名

### 2.1 `:one` — 返回单行

| DSL | Querier 签名 |
|-----|-------------|
| `-- model: Role` | `(ctx, args...) (*Role, error)` |
| `-- model int64` | `(ctx, args...) (int64, error)` |
| `-- model string` | `(ctx, args...) (string, error)` |
| `-- model { total int64 }` | `(ctx, args...) (*MethodName, error)` |

### 2.2 `:many` — 返回多行

| DSL | Querier 签名 |
|-----|-------------|
| `-- model: Role` | `(ctx, args...) ([]*Role, error)` |
| `-- model string` | `(ctx, args...) ([]string, error)` |

### 2.3 `:exec` — 执行无返回值

```go
(ctx, args...) error
```

### 2.4 `:execrows` — 执行返回影响行数

```go
(ctx, args...) (int64, error)
```

## 3. SQL 语句 → 模式

### 3.1 SELECT

```
-- name: FindByID :one
-- model: Role
SELECT id, name FROM roles WHERE id = @id

-- name: FindAll :many
-- model: Role
SELECT id, name FROM roles ORDER BY id

-- name: CountRoles :one
-- model int64
SELECT COUNT(*) FROM roles
```

Querier 使用 `sqlgen.QueryOne`（`:one`）或 `sqlgen.Query`（`:many`），四方言统一。

### 3.2 INSERT

```
-- 无 RETURNING
-- name: InsertRole :exec
INSERT INTO roles (name) VALUES (@name)
→ (ctx, name string) error

-- 有 RETURNING 单列
-- name: InsertRole :one
-- model int64
INSERT INTO roles (name) VALUES (@name)
RETURNING id
→ (ctx, name string) (int64, error)

-- 有 RETURNING * 
-- name: InsertRoleFull :one
-- model: Role
INSERT INTO roles (name) VALUES (@name)
RETURNING *
→ (ctx, name string) (*Role, error)
```

引擎内部实现：

| 引擎 | `:exec` | `:one` + RETURNING |
|------|---------|-------------------|
| PG | `sqlgen.Exec` | `QueryRowContext → Scan` |
| MSSQL | 同 PG | 同 PG |
| Oracle | 同 PG | `ExecContext(args..., Out{Dest})` |
| MySQL | 同 PG | `Exec → LastInsertId()` / `Exec → SELECT` |

### 3.3 UPDATE

```
-- 无 RETURNING
-- param: name string, id int64
-- name: UpdateRole :exec
UPDATE roles SET name = @name WHERE id = @id
→ (ctx, name string, id int64) error

-- 有 RETURNING
-- name: UpdateRole :one
-- model: Role
UPDATE roles SET name = @name WHERE id = @id
RETURNING id, name
→ (ctx, name string, id int64) (*Role, error)
```

MySQL 引擎处理：`SELECT ... FOR UPDATE` → `Exec` → 返回 SELECT 结果。要求调用方在事务内。

### 3.4 DELETE

```
-- 无 RETURNING
-- param: id int64
-- name: DeleteRole :execrows
DELETE FROM roles WHERE id = @id
→ (ctx, id int64) (int64, error)

-- 有 RETURNING
-- name: DeleteRole :one
-- model: Role
DELETE FROM roles WHERE id = @id
RETURNING id, name
→ (ctx, id int64) (*Role, error)
```

MySQL 引擎处理同 UPDATE：`SELECT ... FOR UPDATE` → `Exec` → 返回 SELECT 结果。

## 4. 参数引用

| 写法 | 含义 | 示例 |
|------|------|------|
| `@paramName` | 基本类型参数 | `@id` → `id int64` |
| `@paramName.field` | model 类型参数 | `@filter.name` → `filter.Name string` |

## 5. SQL 特性支持

### 5.1 子句

| 子句 | 示例 | 状态 |
|------|------|------|
| WHERE | `WHERE id = @id` | ✅ |
| AND/OR | `WHERE a = @a AND b = @b` | ✅ |
| ORDER BY | `ORDER BY id DESC` | ✅ |
| LIMIT/OFFSET | `LIMIT @limit OFFSET @offset` | ✅ |
| GROUP BY | `GROUP BY status` | ✅ |
| HAVING | `HAVING COUNT(*) > @n` | ✅ |
| JOIN | `LEFT JOIN orders o ON u.id = o.user_id` | ✅ |
| FOR UPDATE | `SELECT ... FOR UPDATE` | ✅ |
| DISTINCT | `SELECT DISTINCT status` | ✅ |
| IN | `WHERE id IN (@id1, @id2)` | ✅ |
| IS NULL | `WHERE deleted_at IS NULL` | ✅ |
| BETWEEN | `WHERE created_at BETWEEN @start AND @end` | ✅ |
| CAST | `CAST(price AS integer)` | ✅ |
| 子查询 | `WHERE id IN (SELECT ...)` | ✅ |

### 5.2 RETURNING

| 特性 | 示例 | 状态 |
|------|------|------|
| INSERT RETURNING | `INSERT ... RETURNING id` | ✅ |
| INSERT RETURNING * | `INSERT ... RETURNING *` | ✅ 生成时展开 |
| UPDATE RETURNING | `UPDATE ... RETURNING id, name` | ✅ |
| DELETE RETURNING | `DELETE ... RETURNING id, name` | ✅ |

### 5.3 多行 VALUES

```
INSERT INTO roles (name, created_at, updated_at)
VALUES (@n1, @t1, @t2), (@n2, @t3, @t4)
```
✅ 支持（`Values: [][]Expr`）

### 5.4 INSERT ... SELECT

```
INSERT INTO audit_log (user_id, action)
SELECT id, @action FROM users WHERE status = @status
```
✅ 支持（`InsertStmt.Select`）

## 6. 引擎差异透明化

Querier 不暴露引擎信息。调用方只依赖 interface：

```go
import "path/to/sqlgen/roles"

// 编译时选择引擎
q, _ := roles.NewPG(ctx, db)     // PG
q, _ := roles.NewMSSQL(ctx, db)  // SQL Server
q, _ := roles.NewOracle(ctx, db)// Oracle
q, _ := roles.NewMySQL(ctx, db) // MySQL

// 运行时行为一致
id, err := q.InsertRole(ctx, "admin")
role, err := q.FindRoleByID(ctx, id)
```

四个构造器返回同一个 `roles.Querier`。

## 7. DSL ↔ 生成文件映射

```
roles.sql
  │
  ├── roles.go            ← Querier interface
  ├── models.go           ← model structs
  ├── roles.ast.go        ← AST 语句（Statement 结构，无 SQL 字面量）
  ├── roles.sql.pg.go     ← PG 引擎实现 + SQL 常量
  ├── roles.sql.mssql.go  ← MSSQL 引擎实现 + SQL 常量
  ├── roles.sql.oracle.go ← Oracle 引擎实现 + SQL 常量
  └── roles.sql.mysql.go  ← MySQL 引擎实现 + SQL 常量
```
