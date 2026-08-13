# PG 引擎

## 占位符

```
@id → $1, @name → $2
```

## RETURNING

仅支持 INSERT 单列 RETURNING。原生渲染，无转换。

```sql
INSERT INTO users (name) VALUES ($1) RETURNING id
```

执行方式：`PrepareContext → QueryRowContext → Scan`

## ON CONFLICT

原生支持。

```sql
ON CONFLICT (col) DO NOTHING   →  不变
ON CONFLICT (col) DO UPDATE SET →  不变
```

## LIMIT / OFFSET

```sql
LIMIT @limit OFFSET @offset  →  LIMIT $1 OFFSET $2
```

## ILIKE

PG 原生 ILIKE。预处理阶段将 ILIKE 替换为 LIKE 做 AST 解析，渲染时恢复为 ILIKE。

## NOW / BOOLEAN

```sql
NOW()  →  NOW()
TRUE   →  TRUE
FALSE  →  FALSE
```

## FROM dual

vitess 对无 FROM 的 SELECT 会加上 `FROM dual`。PG 引擎渲染后自动移除。

## 数组参数

```sql
WHERE id = ANY(@ids)  →  id = ANY($1)
```

- Go 绑定 `pq.Array(ids)`；Java 绑定 `java.sql.Array`（`createArrayOf("bigint", boxed)`）
- 空数组返回空结果（PG 原生支持 `= ANY('{}')`）

## Runner 实现

```go
type usersRunnerFactoryPG struct{}

func (f *usersRunnerFactoryPG) newFindByID(db *sql.DB) findByIDRunner {
    return &findByIDPG{db: db}
}

type findByIDPG struct {
    stmt *sql.Stmt
    db   *sql.DB
}

func (r *findByIDPG) query(ctx context.Context, id int64) (*sql.Row, error) {
    if r.stmt == nil { r.stmt, _ = r.db.PrepareContext(ctx, findByIDConstPG) }
    return r.stmt.QueryRowContext(ctx, id), nil
}
```

- Runner 方法签名类型化，与 Querier 接口一致
- `*sql.Stmt` lazy prepare，首次调用时初始化
- `close()` 有 nil guard
