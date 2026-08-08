# PG 引擎实现方案

## 1. 占位符

```
DSL:    @id → $1, @name → $2
生成:   $1, $2, $3...
```

## 2. RETURNING

PG 原生支持，直接渲染。

| DSL | 生成 |
|-----|------|
| `RETURNING id` | `RETURNING id` |
| `RETURNING id, name` | `RETURNING id, name` |
| `RETURNING *` | `RETURNING *` |

**执行方式**：`PrepareContext` → `QueryRowContext` → `rows.Scan`

## 3. ON CONFLICT

PG 原生支持。

| DSL | 生成 |
|-----|------|
| `ON CONFLICT (col) DO NOTHING` | 不变 |
| `ON CONFLICT (col) DO UPDATE SET` | 不变 |

**执行方式**：`ExecContext`（无 RETURNING）或 `QueryRowContext`（有 RETURNING）

## 4. LIMIT / OFFSET

```sql
LIMIT @limit OFFSET @offset  →  LIMIT $1 OFFSET $2
```

## 5. ILIKE

PG 原生支持。

```sql
WHERE name ILIKE @pattern  →  WHERE name ILIKE $1
```

## 6. COALESCE

```sql
COALESCE(@status, status)  →  COALESCE($1, status)
```

## 7. NOW()

```sql
NOW()  →  NOW()
```

## 8. BOOLEAN

```sql
TRUE / FALSE  →  TRUE / FALSE
```

## 9. LIKE

```sql
WHERE name LIKE @pattern  →  WHERE name LIKE $1
```

## 10. CTE

```sql
WITH cte AS (...) SELECT ...  →  不变
WITH RECURSIVE cte AS (...)  →  不变
```

## 11. 字符串拼接

```sql
name || ' - ' || code  →  name || ' - ' || code
```

## 12. 当前时间

```sql
NOW()  →  NOW()
CURRENT_DATE  →  CURRENT_DATE
```

## 13. 数组

```sql
WHERE id = ANY(@ids)  →  WHERE id = ANY($1)
```

## 14. NULL 排序

```sql
ORDER BY c NULLS FIRST  →  不变
ORDER BY c NULLS LAST   →  不变
```

## 15. 执行方式

| 操作 | Go 实现 |
|------|---------|
| SELECT :one | `PrepareContext → QueryRowContext → Scan` |
| SELECT :many | `PrepareContext → QueryContext → rows.Scan` |
| INSERT :exec | `PrepareContext → ExecContext` |
| INSERT :one (RETURNING) | `PrepareContext → QueryRowContext → Scan` |
| UPDATE :exec | `PrepareContext → ExecContext` |
| UPDATE :one (RETURNING) | `PrepareContext → QueryRowContext → Scan` |
| DELETE :execrows | `PrepareContext → ExecContext → RowsAffected` |
| DELETE :one (RETURNING) | `PrepareContext → QueryRowContext → Scan` |

## 16. 生成文件

```
roles.sql.pg.go
  - SQL 常量（PG 方言）
  - queriesPG struct
  - NewPG 构造器
  - 方法体
```
