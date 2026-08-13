# MySQL 引擎

## 占位符

```
@id → ?, @name → ?
所有参数统一 ?，无编号。
```

## RETURNING

MySQL 无 RETURNING 语法。仅支持 INSERT 单列 RETURNING，拆为两步：

```sql
-- Step 1: INSERT
INSERT INTO roles (name) VALUES (?)

-- Step 2: SELECT LAST_INSERT_ID()
SELECT LAST_INSERT_ID()
```

Runner 含两个 `*sql.Stmt`（`execStmt` + `queryStmt`），执行时先 Exec 再 QueryRow。

## ON CONFLICT

| DSL | 生成 |
|-----|------|
| `ON CONFLICT (c) DO NOTHING` | `INSERT IGNORE INTO ...` |
| `ON CONFLICT (c) DO UPDATE SET` | `ON DUPLICATE KEY UPDATE ...` |

## LIMIT / OFFSET

```sql
LIMIT @limit OFFSET @offset  →  LIMIT ? OFFSET ?
```

## ILIKE

无原生 ILIKE，翻译为 `LOWER LIKE LOWER`：

```sql
WHERE name ILIKE @pattern  →  WHERE LOWER(name) LIKE LOWER(?)
```

## COALESCE

两参数 COALESCE 翻译为 IFNULL：

```sql
COALESCE(@status, status)  →  IFNULL(?, status)
```

## NOW / BOOLEAN

```sql
NOW()  →  NOW()
TRUE   →  1
FALSE  →  0
```

## RETURNING 剥离

渲染后自动移除 SQL 中的 `RETURNING col` 子句（MySQL 不支持）。

## FROM dual

移除 vitess 引入的 `FROM dual`。

## 数组参数

```sql
WHERE id = ANY(@ids)  →  JSON_CONTAINS(?, CAST(id AS JSON))
```

- Go 绑定 JSON 数组字符串；Java 绑定 Jackson JSON 字符串
- 空数组 `JSON_CONTAINS('[]', ...)` 返回空结果

## Runner 实现

```go
type usersRunnerFactoryMySQL struct{}

func (f *usersRunnerFactoryMySQL) newInsertRole(db *sql.DB) insertRoleRunner {
    return &insertRoleMySQL{db: db}
}

type insertRoleMySQL struct {
    execStmt  *sql.Stmt
    queryStmt *sql.Stmt
    db        *sql.DB
}

const insertRoleConstMySQL = `INSERT INTO roles (name) VALUES (?)`
const insertRoleSelectConstMySQL = `SELECT LAST_INSERT_ID()`

func (r *insertRoleMySQL) execReturning(ctx context.Context, name string) (int64, error) {
    if r.execStmt == nil { r.execStmt, _ = r.db.PrepareContext(ctx, insertRoleConstMySQL) }
    if r.queryStmt == nil { r.queryStmt, _ = r.db.PrepareContext(ctx, insertRoleSelectConstMySQL) }
    r.execStmt.ExecContext(ctx, name)
    var id int64
    r.queryStmt.QueryRowContext(ctx).Scan(&id)
    return id, nil
}
```

- RETURNING Runner 含两个 stmt（exec + query）
- `close()` 依次关闭两个 stmt，均有 nil guard
