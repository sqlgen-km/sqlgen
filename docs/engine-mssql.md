# SQL Server 引擎

引擎框架已完整实现，Golden 测试通过。尚未跑实际数据库集成测试。

## 占位符

```
@id → @p1, @name → @p2
```

## RETURNING

SQL Server 用 `OUTPUT` 子句替代。仅支持 INSERT 单列 RETURNING。

```sql
INSERT INTO roles (name) OUTPUT INSERTED.id VALUES (@p1)
```

UPDATE/DELETE RETURNING 已移除。执行方式：`PrepareContext → QueryRowContext → Scan`

## ON CONFLICT

无原生 ON CONFLICT，用 MERGE 替代：

```sql
MERGE roles AS target
USING (VALUES (@p1, @p2)) AS source (name, created_at)
ON target.name = source.name
WHEN NOT MATCHED THEN INSERT (name, created_at) VALUES (source.name, source.created_at)
WHEN MATCHED THEN UPDATE SET created_at = source.created_at;
```

## LIMIT / OFFSET

```sql
LIMIT @limit OFFSET @offset  →  OFFSET @p1 ROWS FETCH NEXT @p2 ROWS ONLY
```

## ILIKE

无原生 ILIKE，翻译为 `LOWER LIKE LOWER`。

## NOW / BOOLEAN

```sql
NOW()  →  GETDATE()
TRUE   →  1
FALSE  →  0
```

## FROM dual

移除 vitess 引入的 `FROM dual`。

## 数组参数

```sql
WHERE id = ANY(@ids)  →  id IN (SELECT value FROM OPENJSON(@p1))
```

- Go 绑定 JSON 数组字符串；Java 绑定 Jackson JSON 字符串
- 空数组 `OPENJSON('[]')` 返回 0 行（天然正确，故不用 `STRING_SPLIT('')`）

## Runner 实现

```go
type usersRunnerFactoryMSSQL struct{}

func (f *usersRunnerFactoryMSSQL) newFindByID(db *sql.DB) findByIDRunner {
    return &findByIDMSSQL{db: db}
}

type findByIDMSSQL struct {
    stmt *sql.Stmt
    db   *sql.DB
}

func (r *findByIDMSSQL) query(ctx context.Context, id int64) (*sql.Row, error) {
    if r.stmt == nil { r.stmt, _ = r.db.PrepareContext(ctx, findByIDConstMSSQL) }
    return r.stmt.QueryRowContext(ctx, id), nil
}
```

- Runner 类型化签名，与 PG 引擎相同模式
- `close()` 有 nil guard
- 无 RETURNING 多列支持（同其他三引擎）
