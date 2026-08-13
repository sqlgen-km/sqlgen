# sqlgen 方言差异对比

## 1. 占位符

| 场景 | PG | MSSQL | Oracle | MySQL |
|------|-----|-------|--------|-------|
| 参数占位符 | `$1, $2` | `@p1, @p2` | `:1, :2` | `?` |

```
PG:     SELECT id FROM users WHERE name = $1
MSSQL:  SELECT id FROM users WHERE name = @p1
Oracle: SELECT id FROM users WHERE name = :1
MySQL:  SELECT id FROM users WHERE name = ?
```

## 2. INSERT RETURNING

| 场景 | PG | MSSQL | Oracle | MySQL |
|------|-----|-------|--------|-------|
| 单列 | `RETURNING id` | `OUTPUT INSERTED.id` | `RETURNING id INTO :out` | 无语法 |
| 多列 | `RETURNING id, name` | `OUTPUT INSERTED.id, INSERTED.name` | `RETURNING id, name INTO :out1, :out2` | 无语法 |
| 全列 `*` | `RETURNING *` | `OUTPUT INSERTED.*` | `RETURNING * INTO :outN` | 无语法 |
| 执行方式 | `QueryRowContext` | 同 PG | `ExecContext(args..., Out{Dest})` | `Exec` → `SELECT LAST_INSERT_ID()` |

```
PG:     INSERT INTO users (name) VALUES ($1) RETURNING id
MSSQL:  INSERT INTO users (name) VALUES (@p1) OUTPUT INSERTED.id
Oracle: INSERT INTO users (name) VALUES (:1) RETURNING id INTO :2
MySQL:  INSERT INTO users (name) VALUES (?)
        SELECT LAST_INSERT_ID()
```

## 3. UPDATE RETURNING

| 场景 | PG | MSSQL | Oracle | MySQL |
|------|-----|-------|--------|-------|
| 语法 | `RETURNING c` | `OUTPUT INSERTED.c` | `RETURNING c INTO :out` | `SELECT FOR UPDATE` → `UPDATE` → 返回 SELECT 结果 |

```
PG:     UPDATE users SET name = $1 WHERE id = $2 RETURNING id, name
MSSQL:  UPDATE users SET name = @p1 OUTPUT INSERTED.id, INSERTED.name WHERE id = @p2
Oracle: UPDATE users SET name = :1 WHERE id = :2 RETURNING id, name INTO :3, :4
MySQL:  SELECT id, name FROM users WHERE id = ? FOR UPDATE
        UPDATE users SET name = ? WHERE id = ?
```

## 4. DELETE RETURNING

| 场景 | PG | MSSQL | Oracle | MySQL |
|------|-----|-------|--------|-------|
| 语法 | `RETURNING c` | `OUTPUT DELETED.c` | `RETURNING c INTO :out` | `SELECT FOR UPDATE` → `DELETE` → 返回 SELECT 结果 |

```
PG:     DELETE FROM users WHERE id = $1 RETURNING id, name
MSSQL:  DELETE FROM users OUTPUT DELETED.id, DELETED.name WHERE id = @p1
Oracle: DELETE FROM users WHERE id = :1 RETURNING id, name INTO :2, :3
MySQL:  SELECT id, name FROM users WHERE id = ? FOR UPDATE
        DELETE FROM users WHERE id = ?
```

## 5. UPSERT（ON CONFLICT）

| 场景 | PG | MSSQL | Oracle | MySQL |
|------|-----|-------|--------|-------|
| DO NOTHING | `ON CONFLICT (c) DO NOTHING` | `MERGE ... WHEN NOT MATCHED THEN INSERT` | 同 MSSQL | `INSERT IGNORE` |
| DO UPDATE | `ON CONFLICT (c) DO UPDATE SET` | `MERGE ... WHEN MATCHED THEN UPDATE` | 同 MSSQL | `ON DUPLICATE KEY UPDATE` |

```
PG:     INSERT INTO t (id, name) VALUES ($1, $2)
        ON CONFLICT (id) DO NOTHING

MSSQL:  MERGE t AS target
        USING (VALUES (@p1, @p2)) AS source (id, name)
        ON target.id = source.id
        WHEN NOT MATCHED THEN INSERT (id, name) VALUES (source.id, source.name);

MySQL:  INSERT INTO t (id, name) VALUES (?, ?)
        ON DUPLICATE KEY UPDATE name = VALUES(name)
```

## 6. LIMIT / OFFSET

| 场景 | PG | MSSQL | Oracle | MySQL |
|------|-----|-------|--------|-------|
| 语法 | `LIMIT n OFFSET m` | 同 PG | `OFFSET m ROWS FETCH NEXT n ROWS ONLY` | `LIMIT n OFFSET m` |

```
PG:     SELECT id FROM users ORDER BY id LIMIT 10 OFFSET 20
MSSQL:  SELECT id FROM users ORDER BY id LIMIT 10 OFFSET 20
Oracle: SELECT id FROM users ORDER BY id OFFSET 20 ROWS FETCH NEXT 10 ROWS ONLY
MySQL:  SELECT id FROM users ORDER BY id LIMIT 10 OFFSET 20
```

## 7. 大小写函数

| 场景 | PG | MSSQL | Oracle | MySQL |
|------|-----|-------|--------|-------|
| 小写 | `LOWER(x)` | `LOWER(x)` | `LOWER(x)` | `LOWER(x)` |
| 大写 | `UPPER(x)` | `UPPER(x)` | `UPPER(x)` | `UPPER(x)` |

四方言一致。

## 8. LIKE 模式匹配

| 场景 | PG | MSSQL | Oracle | MySQL |
|------|-----|-------|--------|-------|
| 语法 | `LIKE` / `NOT LIKE` | 同 | 同 | 同 |
| 大小写不敏感 | `LOWER(x) LIKE LOWER(p)` | 同 | 同 | 同 |
| 通配符 | `%` `_` | `%` `_` | `%` `_` | `%` `_` |

四方言一致。

## 9. COALESCE

| 场景 | PG | MSSQL | Oracle | MySQL |
|------|-----|-------|--------|-------|
| 语法 | `COALESCE(a, b)` | `COALESCE(a, b)` | `NVL(a, b)` | `IFNULL(a, b)` |

```
PG:     SELECT COALESCE(@uid, user_id) FROM users
MSSQL:  SELECT COALESCE(@p1, user_id) FROM users
Oracle: SELECT NVL(:1, user_id) FROM users
MySQL:  SELECT IFNULL(?, user_id) FROM users
```

## 10. CAST

| 场景 | PG | MSSQL | Oracle | MySQL |
|------|-----|-------|--------|-------|
| 语法 | `CAST(x AS type)` | `CAST(x AS type)` | `CAST(x AS type)` | `CAST(x AS type)` |

四方言一致。

## 11. 当前时间

| 场景 | PG | MSSQL | Oracle | MySQL |
|------|-----|-------|--------|-------|
| 当前时间戳 | `NOW()` | `GETDATE()` | `SYSDATE` | `NOW()` |
| 当前日期 | `CURRENT_DATE` | `GETDATE()` | `TRUNC(SYSDATE)` | `CURDATE()` |

```
PG:     UPDATE users SET updated_at = NOW()
MSSQL:  UPDATE users SET updated_at = GETDATE()
Oracle: UPDATE users SET updated_at = SYSDATE
MySQL:  UPDATE users SET updated_at = NOW()
```

## 12. 自增 ID 获取

| 场景 | PG | MSSQL | Oracle | MySQL |
|------|-----|-------|--------|-------|
| 方式 | `RETURNING id` | `OUTPUT INSERTED.id` | `RETURNING id INTO :out` | `LAST_INSERT_ID()` |
| 序列名 | `SERIAL` / `IDENTITY` | `IDENTITY` | `SEQUENCE` + `TRIGGER` | `AUTO_INCREMENT` |

## 13. BOOLEAN

| 场景 | PG | MSSQL | Oracle | MySQL |
|------|-----|-------|--------|-------|
| 字面量 | `TRUE` / `FALSE` | `1` / `0` | `1` / `0` | `TRUE` / `FALSE` 或 `1` / `0` |
| 列类型 | `BOOLEAN` | `BIT` | `NUMBER(1)` | `TINYINT(1)` |

```
PG:     UPDATE users SET is_active = TRUE
MSSQL:  UPDATE users SET is_active = 1
Oracle: UPDATE users SET is_active = 1
MySQL:  UPDATE users SET is_active = TRUE
```

## 14. 数组类型

| 场景 | PG | MSSQL | Oracle | MySQL |
|------|-----|-------|--------|-------|
| 列类型 | `TEXT[]` / `INT[]` | 无原生数组 | `VARRAY` / 嵌套表 | `JSON` |
| IN 查询 | `ANY(@arr)` | `IN (SELECT value FROM OPENJSON(...))` | `MEMBER OF` | `JSON_CONTAINS` |

```
PG:     WHERE id = ANY(@ids)
MSSQL:  WHERE id IN (SELECT value FROM OPENJSON(@ids))
Oracle: WHERE id MEMBER OF @ids
MySQL:  WHERE JSON_CONTAINS(@ids, CAST(id AS JSON))
```

当前 DSL 中数组参数 (`[]int64`, `[]string`) 主要面向 PG 的 `ANY(@arr)` 模式。

## 15. NULL 排序

| 场景 | PG | MSSQL | Oracle | MySQL |
|------|-----|-------|--------|-------|
| NULLS FIRST | `ORDER BY c NULLS FIRST` | `ORDER BY CASE WHEN c IS NULL THEN 0 ELSE 1 END, c` | `ORDER BY c NULLS FIRST` | `ORDER BY c IS NULL, c` |
| NULLS LAST | `ORDER BY c NULLS LAST` | `ORDER BY CASE WHEN c IS NULL THEN 1 ELSE 0 END, c` | `ORDER BY c NULLS LAST` | `ORDER BY c IS NOT NULL, c` |

## 16. 字符串拼接

| 场景 | PG | MSSQL | Oracle | MySQL |
|------|-----|-------|--------|-------|
| 语法 | `a \|\| b` | `a + b` | `a \|\| b` | `CONCAT(a, b)` |

```
PG:     SELECT name || ' - ' || code FROM users
MSSQL:  SELECT name + ' - ' + code FROM users
Oracle: SELECT name || ' - ' || code FROM users
MySQL:  SELECT CONCAT(name, ' - ', code) FROM users
```

## 17. 子查询

| 场景 | PG | MSSQL | Oracle | MySQL |
|------|-----|-------|--------|-------|
| 语法 | `IN (SELECT ...)` / `EXISTS (SELECT ...)` | 同 | 同 | 同 |

四方言一致。

## 18. CTE（公共表表达式）

| 场景 | PG | MSSQL | Oracle | MySQL |
|------|-----|-------|--------|-------|
| 非递归 | ✅ | ✅ | ✅ (9i+) | ✅ (8.0+) |
| 递归 | ✅ `RECURSIVE` | ✅ 无关键字 | ✅ `RECURSIVE` | ✅ `RECURSIVE` (8.0+) |
| 多 CTE | ✅ 逗号分隔 | ✅ | ✅ | ✅ |
| INSERT ... WITH | ✅ | ✅ | ✅ | ✅ |
| UPDATE ... WITH | ✅ | ✅ | ✅ | ✅ |
| DELETE ... WITH | ✅ | ✅ | ✅ | ✅ |

```
-- 四方言语法完全一致
PG/MSSQL/Oracle/MySQL:
WITH active AS (
    SELECT id, name FROM users WHERE status = 'active'
)
SELECT * FROM active WHERE name LIKE @pattern

-- 递归 CTE
WITH RECURSIVE tree AS (
    SELECT id, parent_id, name FROM nodes WHERE parent_id IS NULL
    UNION ALL
    SELECT n.id, n.parent_id, n.name FROM nodes n
    JOIN tree t ON n.parent_id = t.id
)
SELECT * FROM tree
```

唯一四方言语法完全一致的高级特性。

### 18.1 引擎限制

| 引擎 | 递归深度 | 备注 |
|------|---------|------|
| PG | 无限制 | `WITH RECURSIVE` |
| MSSQL | 默认 100 | `OPTION (MAXRECURSION n)` |
| Oracle | 无限制 | `WITH` + `CONNECT BY` 两种方式 |
| MySQL | 默认 1000 | `cte_max_recursion_depth` |

## 19. 执行方式总结

| 操作 | PG | MSSQL | Oracle | MySQL |
|------|-----|-------|--------|-------|
| INSERT | `QueryRowContext` / `ExecContext` | 同 PG | `ExecContext` + `sql.Out` | `ExecContext` + 额外查询 |
| UPDATE (无 RETURNING) | `ExecContext` | 同 PG | 同 PG | 同 PG |
| UPDATE (有 RETURNING) | `QueryRowContext` | 同 PG | `ExecContext` + `sql.Out` | `SELECT FOR UPDATE` + `ExecContext` |
| DELETE (无 RETURNING) | `ExecContext` | 同 PG | 同 PG | 同 PG |
| DELETE (有 RETURNING) | `QueryRowContext` | 同 PG | `ExecContext` + `sql.Out` | `SELECT FOR UPDATE` + `ExecContext` |
| SELECT | `QueryContext` / `QueryRowContext` | 同 PG | 同 PG | 同 PG |
