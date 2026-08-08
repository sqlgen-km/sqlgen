# SQL Server 引擎实现方案

## 1. 占位符

```
DSL:    @id → @p1, @name → @p2
生成:   @p1, @p2, @p3...
```

## 2. RETURNING

SQL Server 用 `OUTPUT` 子句替代。

| DSL | 生成 |
|-----|------|
| `RETURNING id` | `OUTPUT INSERTED.id` |
| `RETURNING id, name` | `OUTPUT INSERTED.id, INSERTED.name` |
| `RETURNING *` | `OUTPUT INSERTED.*` |
| UPDATE RETURNING | `OUTPUT INSERTED.id, INSERTED.name` |
| DELETE RETURNING | `OUTPUT DELETED.id, DELETED.name` |

**注意**：OUTPUT 放在 VALUES/WHERE 之前，不是末尾。

```sql
-- INSERT
INSERT INTO roles (name) OUTPUT INSERTED.id VALUES (@p1)

-- UPDATE
UPDATE roles SET name = @p1 OUTPUT INSERTED.id, INSERTED.name WHERE id = @p2

-- DELETE
DELETE FROM roles OUTPUT DELETED.id, DELETED.name WHERE id = @p1
```

**执行方式**：`PrepareContext` → `QueryRowContext` → `rows.Scan`（同 PG）

## 3. ON CONFLICT

SQL Server 无原生 ON CONFLICT，用 `MERGE` 替代。

| DSL | 生成 |
|-----|------|
| `ON CONFLICT (c) DO NOTHING` | `MERGE ... WHEN NOT MATCHED THEN INSERT ...` |
| `ON CONFLICT (c) DO UPDATE SET` | `MERGE ... WHEN MATCHED THEN UPDATE SET ...` |

```sql
MERGE roles AS target
USING (VALUES (@p1, @p2)) AS source (name, created_at)
ON target.name = source.name
WHEN NOT MATCHED THEN
    INSERT (name, created_at) VALUES (source.name, source.created_at)
WHEN MATCHED THEN
    UPDATE SET created_at = source.created_at
OUTPUT INSERTED.id;      -- 如果有 RETURNING
```

**执行方式**：`QueryRowContext`（有 RETURNING）/ `ExecContext`（无 RETURNING）

## 4. LIMIT / OFFSET

```sql
LIMIT @limit OFFSET @offset  →  LIMIT @p1 OFFSET @p2
```

不支持 `LIMIT @p1, @p2` 语法，用 `OFFSET ... FETCH NEXT` 或 `ORDER BY OFFSET ... FETCH NEXT`。

```sql
-- 兼容写法
OFFSET @p1 ROWS FETCH NEXT @p2 ROWS ONLY
```

## 5. ILIKE

无原生 ILIKE，翻译为 LOWER + LIKE。

```sql
WHERE name ILIKE @pattern  →  WHERE LOWER(name) LIKE LOWER(@p1)
```

## 6. COALESCE

```sql
COALESCE(@status, status)  →  COALESCE(@p1, status)
```

## 7. NOW()

```sql
NOW()  →  GETDATE()
CURRENT_DATE  →  CAST(GETDATE() AS DATE)
```

## 8. BOOLEAN

```sql
TRUE / FALSE  →  1 / 0
```

## 9. LIKE

```sql
WHERE name LIKE @pattern  →  WHERE name LIKE @p1
```

## 10. CTE

```sql
WITH cte AS (...) SELECT ...  →  不变
递归: WITH cte AS (anchor UNION ALL recursive)  →  不变（无 RECURSIVE 关键字）
```

**限制**：递归深度默认 100，用 `OPTION (MAXRECURSION n)`。

## 11. 字符串拼接

```sql
name || ' - ' || code  →  name + ' - ' + code
```

## 12. 当前时间

```sql
NOW()  →  GETDATE()
```

## 13. 数组

无原生数组。`IN` 查询可用 `STRING_SPLIT`，但参数化困难，建议应用层处理。

## 14. NULL 排序

无 NULLS FIRST/LAST 语法，用 CASE 表达式。

```sql
ORDER BY c NULLS FIRST  →  ORDER BY CASE WHEN c IS NULL THEN 0 ELSE 1 END, c
ORDER BY c NULLS LAST   →  ORDER BY CASE WHEN c IS NULL THEN 1 ELSE 0 END, c
```

## 15. 执行方式

| 操作 | Go 实现 |
|------|---------|
| SELECT :one | `PrepareContext → QueryRowContext → Scan` |
| SELECT :many | `PrepareContext → QueryContext → rows.Scan` |
| INSERT :exec | `PrepareContext → ExecContext` |
| INSERT :one (RETURNING) | `PrepareContext → QueryRowContext → Scan` |
| INSERT ON CONFLICT + RETURNING | `PrepareContext → QueryRowContext(MERGE) → Scan` |
| UPDATE :exec | `PrepareContext → ExecContext` |
| UPDATE :one (RETURNING) | `PrepareContext → QueryRowContext → Scan` |
| DELETE :execrows | `PrepareContext → ExecContext → RowsAffected` |
| DELETE :one (RETURNING) | `PrepareContext → QueryRowContext → Scan` |

## 16. 生成文件

```
roles.sql.mssql.go
  - SQL 常量（MSSQL 方言）
  - queriesMSSQL struct（与 PG 字段结构相同）
  - NewMSSQL 构造器
  - 方法体
```
