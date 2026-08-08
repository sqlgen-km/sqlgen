# Oracle 引擎实现方案

## 1. 占位符

```
DSL:    @id → :1, @name → :2
生成:   :1, :2, :3...
```

## 2. RETURNING

Oracle 用 `RETURNING ... INTO` 语法。

| DSL | 生成 |
|-----|------|
| `RETURNING id` | `RETURNING id INTO :out_0` |
| `RETURNING id, name` | `RETURNING id, name INTO :out_0, :out_1` |
| `RETURNING *` | 展开为 model 列 → `RETURNING id, name, ... INTO :out_0, :out_1, ...` |

```sql
-- INSERT
INSERT INTO roles (name) VALUES (:1) RETURNING id INTO :2

-- UPDATE
UPDATE roles SET name = :1 WHERE id = :2 RETURNING id, name INTO :3, :4

-- DELETE
DELETE FROM roles WHERE id = :1 RETURNING id, name INTO :2, :3
```

**执行方式**：`PrepareContext` → `ExecContext(args..., sql.Out{Dest: &d0}, ...)`

OUT 参数绑定在 IN 参数之后。Go 代码：

```go
var id int64
_, err := stmt.ExecContext(ctx, name, sql.Out{Dest: &id})
```

`RETURNING *` 多列时按 model 字段顺序绑定多个 `sql.Out`。

## 3. ON CONFLICT

Oracle 无 ON CONFLICT，用 `MERGE` 替代。

| DSL | 生成 |
|-----|------|
| `ON CONFLICT (c) DO NOTHING` | `MERGE ... WHEN NOT MATCHED THEN INSERT` |
| `ON CONFLICT (c) DO UPDATE SET` | `MERGE ... WHEN MATCHED THEN UPDATE` |

```sql
MERGE INTO roles t
USING (SELECT :1 AS name, :2 AS created_at FROM dual) s
ON (t.name = s.name)
WHEN NOT MATCHED THEN
    INSERT (name, created_at) VALUES (s.name, s.created_at)
WHEN MATCHED THEN
    UPDATE SET created_at = s.created_at
```

有 RETURNING 时加 `RETURNING id INTO :out_0`。

## 4. LIMIT / OFFSET

```sql
LIMIT @limit OFFSET @offset
→ OFFSET :1 ROWS FETCH NEXT :2 ROWS ONLY
```

## 5. ILIKE

无原生 ILIKE，翻译为 LOWER + LIKE。

```sql
WHERE name ILIKE @pattern  →  WHERE LOWER(name) LIKE LOWER(:1)
```

## 6. COALESCE

Oracle 用 `NVL`（两参数）或 `COALESCE`（Oracle 9i+ 支持）。

```sql
COALESCE(@status, status)  →  COALESCE(:1, status)
-- 或 NVL(:1, status)
```

## 7. NOW()

```sql
NOW()  →  SYSDATE
CURRENT_DATE  →  TRUNC(SYSDATE)
```

## 8. BOOLEAN

```sql
TRUE / FALSE  →  1 / 0
```

## 9. LIKE

```sql
WHERE name LIKE @pattern  →  WHERE name LIKE :1
```

## 10. CTE

```sql
WITH cte AS (...) SELECT ...  →  不变
WITH RECURSIVE cte AS (...)  →  WITH cte (...)
```

Oracle 递归 CTE 语法略有不同，`RECURSIVE` 关键字位置在 `WITH` 后。

## 11. 字符串拼接

```sql
name || ' - ' || code  →  name || ' - ' || code
```

## 12. 当前时间

```sql
NOW()  →  SYSDATE
```

## 13. 数组

Oracle 有 VARRAY 和嵌套表，但参数化复杂。用 `MEMBER OF` 或 `TABLE` 函数。建议应用层处理。

## 14. NULL 排序

```sql
ORDER BY c NULLS FIRST  →  不变
ORDER BY c NULLS LAST   →  不变
```

## 15. SELECT 子查询

`FROM dual` 用于无表查询：

```sql
SELECT 1 FROM users WHERE ... → 正常
SELECT 1                  → SELECT 1 FROM dual
```

## 16. 执行方式

| 操作 | Go 实现 |
|------|---------|
| SELECT :one | `PrepareContext → QueryRowContext → Scan` |
| SELECT :many | `PrepareContext → QueryContext → rows.Scan` |
| INSERT :exec | `PrepareContext → ExecContext` |
| INSERT :one (RETURNING) | `PrepareContext → ExecContext(args..., sql.Out{Dest})` |
| INSERT ON CONFLICT + RETURNING | `PrepareContext → ExecContext(MERGE..., sql.Out{Dest})` |
| UPDATE :exec | `PrepareContext → ExecContext` |
| UPDATE :one (RETURNING) | `PrepareContext → ExecContext(args..., sql.Out{Dest})` |
| DELETE :execrows | `PrepareContext → ExecContext → RowsAffected` |
| DELETE :one (RETURNING) | `PrepareContext → ExecContext(args..., sql.Out{Dest})` |

**关键差异**：RETURNING 不用 `QueryRowContext`，用 `ExecContext` + `sql.Out` 参数绑定输出。

## 17. 生成文件

```
roles.sql.oracle.go
  - SQL 常量（Oracle 方言）
  - queriesOracle struct（字段用 *sql.Stmt 替代 sqlgen.Exec/QueryOne）
  - NewOracle 构造器
  - 方法体（ExecContext + sql.Out）
```
