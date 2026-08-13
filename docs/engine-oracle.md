# Oracle 引擎

## 占位符

```
@id → :1, @name → :2
go-ora 按出现顺序位置绑定，:N 编号仅作标识。
```

## RETURNING

仅支持 INSERT 单列 RETURNING。Oracle 用 `RETURNING ... INTO`，go-ora 用 `ExecContext + &id` 绑定输出。

```sql
INSERT INTO roles (name) VALUES (:1) RETURNING id INTO :2
```

```go
func (r *insertRoleOracle) execReturning(ctx context.Context, name string) (int64, error) {
    var id int64
    r.stmt.ExecContext(ctx, name, &id)
    return id, nil
}
```

go-ora 不支持多列 RETURNING（混合类型报 ORA-03146）。仅单列 `RETURNING id` 可用。

## ON CONFLICT

Oracle 无 ON CONFLICT，用 MERGE 替代：

```sql
INSERT INTO users (name, price) VALUES (:1, :2)
ON CONFLICT (name) DO UPDATE SET price = :3

-- 转换为:
MERGE INTO users t
USING (SELECT :1 AS name, :2 AS price FROM dual) s
ON (t.name = s.name)
WHEN NOT MATCHED THEN INSERT (name, price) VALUES (s.name, s.price)
WHEN MATCHED THEN UPDATE SET price = s.price
```

有 RETURNING 时追加 `RETURNING id INTO :out0`。

## LIMIT / OFFSET

Oracle 12c+ 语法，渲染为 `OFFSET` 在前 `FETCH NEXT` 在后：

```sql
LIMIT @page_limit OFFSET @page_offset
→ OFFSET :1 ROWS FETCH NEXT :2 ROWS ONLY
```

go-ora 按出现顺序位置绑定：`:1 = offset, :2 = limit`。但 DSL 参数顺序是 `(limit, offset)`，Runner 内显式交换：

```go
func (r *listPagedOracle) query(ctx context.Context, page_limit int32, page_offset int32) (*sql.Rows, error) {
    return r.stmt.QueryContext(ctx, page_offset, page_limit)
}
```

## ILIKE

无原生 ILIKE，翻译为 `LOWER LIKE LOWER`。

## NOW / BOOLEAN

```sql
NOW()  →  SYSDATE
TRUE   →  1
FALSE  →  0
```

## 数组参数

```sql
WHERE id = ANY(@ids)  →  id IN (SELECT COLUMN_VALUE FROM TABLE(:1))
```

- 用 `TABLE()` 反嵌套而非 `MEMBER OF`：`SYS.ODCINUMBERLIST`/`ODCIVARCHAR2LIST` 是 VARRAY，`MEMBER OF` 只认嵌套表（实测 ORA-00932）
- Go 绑定 `go_ora.Object{Owner:"SYS", Name:"ODCINUMBERLIST", Value: ids}`；Java 绑定 `oracle.sql.ARRAY`（`createARRAY`）
- Go 侧空数组需框架短路（go-ora 绑空/nil 集合触发 ORA-00600 kokbgc2ip1）；Java `createARRAY` 空数组正常返回 0 行

## Runner 实现

```go
type usersRunnerFactoryOracle struct{}

func (f *usersRunnerFactoryOracle) newFindByID(db *sql.DB) findByIDRunner {
    return &findByIDOracle{db: db}
}

type findByIDOracle struct {
    stmt *sql.Stmt
    db   *sql.DB
}

func (r *findByIDOracle) query(ctx context.Context, id int64) (*sql.Row, error) {
    if r.stmt == nil { r.stmt, _ = r.db.PrepareContext(ctx, findByIDConstOracle) }
    return r.stmt.QueryRowContext(ctx, id), nil
}
```

- Runner 方法签名类型化
- `needsOffsetSwap` 检测 LIMIT + OFFSET，生成参数交换代码
- RETURNING 用 `ExecContext` + `&id`，不用 `QueryRowContext`
