# sqlgen 执行器设计

## 1. 执行器接口签名

| 场景 | RunnerKind | 方法签名 |
|------|-----------|---------|
| SELECT :one | `QueryOne` | `query(ctx, args...) (*sql.Row, error)` |
| SELECT :many | `QueryMany` | `query(ctx, args...) (*sql.Rows, error)` |
| :exec | `Exec` | `exec(ctx, args...) (sql.Result, error)` |
| :execrows | `ExecRows` | `exec(ctx, args...) (sql.Result, error)` |
| RETURNING id（单列标量） | `ReturningScalar` | `execReturning(ctx, args...) (int64, error)` |
| RETURNING a,b / RETURNING * | `Returning` | `execReturning(ctx, args...) (*sql.Rows, error)` |

5 种接口，6 个 RunnerKind（Exec/ExecRows 共用 Exec 接口+框架区分 RowsAffected）。

## 2. 各阶段任务

### 阶段一：DSL 解析（parser.go）

输入：`.sql` 文件
输出：`QueryDef`（含 Name、Mode、Params、ReturnType、AST）

关键判断：
- `RETURNING *` → AST 中 `Returning: ["*"]`，后续框架不展开
- `RETURNING id, name` → AST 中 `Returning: ["id","name"]`
- `RETURNING id` → AST 中 `Returning: ["id"]`
- 无 RETURNING → AST 中 `Returning: nil`

### 阶段二：框架分类（generator.go）

输入：`QueryDef`
输出：`RunnerSpec{Kind, Stmt}`

```go
func classify(q QueryDef) RunnerKind {
    if hasReturning(q.Stmt) {
        if len(q.Returning) == 1 && q.IsScalar {
            return RunnerReturningScalar
        }
        return RunnerReturning
    }
    switch q.Mode {
    case "one":  return RunnerQueryOne
    case "many": return RunnerQueryMany
    case "exec": return RunnerExec
    case "execrows": return RunnerExecRows
    }
}
```

### 阶段三：框架生成（generator.go）

输入：`[]RunnerSpec`
输出：共享文件

```
roles.go          → Querier interface + 公共方法体
models.go         → model struct
runner.go         → runner 接口定义（每操作一个 unexported interface）
```

#### 3.1 Runner 接口（框架生成）

```go
// SELECT :one
type findByIdRunner interface {
    query(ctx context.Context, args ...any) (*sql.Row, error)
    close() error
    withTx(tx *sql.Tx) findByIdRunner
}

// SELECT :many
type findAllRunner interface {
    query(ctx context.Context, args ...any) (*sql.Rows, error)
    close() error
    withTx(tx *sql.Tx) findAllRunner
}

// :exec / :execrows
type deleteRoleRunner interface {
    exec(ctx context.Context, args ...any) (sql.Result, error)
    close() error
    withTx(tx *sql.Tx) deleteRoleRunner
}

// RETURNING id（单列标量）
type insertRoleIDRunner interface {
    execReturning(ctx context.Context, args ...any) (int64, error)
    close() error
    withTx(tx *sql.Tx) insertRoleIDRunner
}

// RETURNING a,b / RETURNING *
type insertRoleRunner interface {
    execReturning(ctx context.Context, args ...any) (*sql.Rows, error)
    close() error
    withTx(tx *sql.Tx) insertRoleRunner
}
```

#### 3.2 queries struct（框架生成）

```go
type queries struct {
    db          *sql.DB
    insertRoleID insertRoleIDRunner
    insertRole   insertRoleRunner
    findById     findByIdRunner
    findAll      findAllRunner
    deleteRole   deleteRoleRunner
}
```

#### 3.3 公共方法（框架生成）

```go
// RETURNING id → 直接返回标量
func (q *queries) InsertRoleID(ctx context.Context, name string) (int64, error) {
    return q.insertRoleID.execReturning(ctx, name)
}

// RETURNING id, name → *sql.Rows + Scan
func (q *queries) InsertRole(ctx context.Context, name string) (*Role, error) {
    rows, err := q.insertRole.execReturning(ctx, name)
    if err != nil { return nil, err }
    var r Role
    if err := rows.Scan(&r.ID, &r.Name); err != nil { return nil, rows.Err() }
    return &r, rows.Err()
}

// RETURNING * → *sql.Rows + ScanAuto
func (q *queries) InsertRoleFull(ctx context.Context, name string) (*Role, error) {
    rows, err := q.insertRoleFull.execReturning(ctx, name)
    if err != nil { return nil, err }
    defer rows.Close()
    var r Role
    if err := rows.ScanAuto(&r); err != nil { return nil, rows.Err() }
    return &r, rows.Err()
}

// SELECT :one
func (q *queries) FindByID(ctx context.Context, id int64) (*Role, error) {
    row, err := q.findById.query(ctx, id)
    if err != nil { return nil, err }
    var r Role
    if err := row.Scan(&r.ID, &r.Name); err != nil { return nil, err }
    return &r, nil
}

// SELECT :many
func (q *queries) FindAll(ctx context.Context, limit, offset int32) ([]*Role, error) {
    rows, err := q.findAll.query(ctx, limit, offset)
    if err != nil { return nil, err }
    defer rows.Close()
    var items []*Role
    for rows.Next() {
        var r Role
        if err := rows.Scan(&r.ID, &r.Name); err != nil { return nil, err }
        items = append(items, &r)
    }
    return items, rows.Err()
}

// :exec
func (q *queries) DeleteRole(ctx context.Context, id int64) error {
    _, err := q.deleteRole.exec(ctx, id)
    return err
}

// :execrows
func (q *queries) DeleteRoleRows(ctx context.Context, id int64) (int64, error) {
    result, err := q.deleteRole.exec(ctx, id)
    if err != nil { return 0, err }
    return result.RowsAffected()
}
```

### 阶段四：引擎生成（engines/pg/pg.go 等）

输入：`RunnerSpec`
输出：每引擎的 Runner 实现文件

引擎根据 `spec.Kind` 生成对应的实现 struct + 方法 + SQL 常量。

#### PG 引擎示例

```go
// === files: roles.sql.pg.go ===

const (
    sqlInsertRoleID = "INSERT INTO roles (name) VALUES ($1) RETURNING id"
    sqlInsertRole   = "INSERT INTO roles (name) VALUES ($1) RETURNING id, name"
    sqlFindByID     = "SELECT id, name FROM roles WHERE id = $1"
    sqlFindAll      = "SELECT id, name FROM roles ORDER BY id LIMIT $1 OFFSET $2"
    sqlDeleteRole   = "DELETE FROM roles WHERE id = $1"
)

func NewPG(ctx context.Context, db *sql.DB) (Querier, error) { ... }

// RETURNING id → QueryRowContext → Scan 标量
type insertRoleIDPG struct{ stmt *sql.Stmt }
func (r *insertRoleIDPG) execReturning(ctx context.Context, args ...any) (int64, error) {
    var id int64
    row := r.stmt.QueryRowContext(ctx, args...)
    if err := row.Scan(&id); err != nil { return 0, err }
    return id, nil
}

// RETURNING id,name → QueryRowContext → 返回单行 Rows
type insertRolePG struct{ stmt *sql.Stmt }
func (r *insertRolePG) execReturning(ctx context.Context, args ...any) (*sql.Rows, error) {
    return r.stmt.QueryContext(ctx, args...)
}

// RETURNING * → QueryRowContext → 返回单行 Rows（同多列）
type insertRoleFullPG struct{ stmt *sql.Stmt }
func (r *insertRoleFullPG) execReturning(ctx context.Context, args ...any) (*sql.Rows, error) {
    return r.stmt.QueryContext(ctx, args...)
}

// SELECT :one
type findByIdPG struct{ stmt *sql.Stmt }
func (r *findByIdPG) query(ctx context.Context, args ...any) (*sql.Row, error) {
    return r.stmt.QueryRowContext(ctx, args...), nil
}

// SELECT :many
type findAllPG struct{ stmt *sql.Stmt }
func (r *findAllPG) query(ctx context.Context, args ...any) (*sql.Rows, error) {
    return r.stmt.QueryContext(ctx, args...)
}

// :exec / :execrows
type deleteRolePG struct{ stmt *sql.Stmt }
func (r *deleteRolePG) exec(ctx context.Context, args ...any) (sql.Result, error) {
    return r.stmt.ExecContext(ctx, args...)
}

// close / withTx（每个 runner 都有，省略）
```

#### Oracle 引擎

```go
// RETURNING id → ExecContext + sql.Out
func (r *insertRoleIDOra) execReturning(ctx context.Context, args ...any) (int64, error) {
    var id int64
    _, err := r.stmt.ExecContext(ctx, append(args, sql.Out{Dest: &id})...)
    return id, err
}

// RETURNING id,name → ExecContext + sql.Out + 包装单行 Rows
func (r *insertRoleOra) execReturning(ctx context.Context, args ...any) (*sql.Rows, error) {
    // Oracle 无法返回 *sql.Rows，改两步：INSERT → SELECT
    if _, err := r.stmt.ExecContext(ctx, args...); err != nil { return nil, err }
    return r.selectStmt.QueryContext(ctx)
}

// RETURNING * → 两步查询
func (r *insertRoleFullOra) execReturning(ctx context.Context, args ...any) (*sql.Rows, error) {
    if _, err := r.stmt.ExecContext(ctx, args...); err != nil { return nil, err }
    return r.selectAllStmt.QueryContext(ctx)  // SELECT * FROM roles WHERE id = seq.CURRVAL
}
```

#### MySQL 引擎

```go
// RETURNING id → ExecContext → LAST_INSERT_ID
type insertRoleIDMySQL struct{ stmt, lastID *sql.Stmt }
func (r *insertRoleIDMySQL) execReturning(ctx context.Context, args ...any) (int64, error) {
    if _, err := r.stmt.ExecContext(ctx, args...); err != nil { return 0, err }
    var id int64
    if err := r.lastID.QueryRowContext(ctx).Scan(&id); err != nil { return 0, err }
    return id, nil
}

// RETURNING id,name → Exec → SELECT cols WHERE id = LAST_INSERT_ID()
func (r *insertRoleMySQL) execReturning(ctx context.Context, args ...any) (*sql.Rows, error) {
    if _, err := r.stmt.ExecContext(ctx, args...); err != nil { return nil, err }
    return r.selectStmt.QueryContext(ctx)
}

// RETURNING * → Exec → SELECT * WHERE id = LAST_INSERT_ID()
func (r *insertRoleFullMySQL) execReturning(ctx context.Context, args ...any) (*sql.Rows, error) {
    if _, err := r.stmt.ExecContext(ctx, args...); err != nil { return nil, err }
    return r.selectAllStmt.QueryContext(ctx)
}
```

## 3. 任务分工总结

| 阶段 | 职责 | 输入 | 输出 |
|------|------|------|------|
| **Parser** | 解析 DSL → AST | `.sql` 文件 | `QueryDef{Name, Mode, Params, Stmt}` |
| **Framework** | 分类 + 生成接口/struct/方法体 | `QueryDef` | `runner.go`（接口）、`roles.go`（Querier 公共方法） |
| **Engine** | 生成 Runner 实现 + SQL 常量 | `RunnerSpec{Kind, Stmt}` | `roles.sql.pg.go` 等（impl struct + 构造器 + SQL 常量） |

Framework 永远不知道方言。Engine 永远不知道 Interface。交集在 `RunnerSpec`。
