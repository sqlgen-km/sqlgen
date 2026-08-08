# sqlgen — SQL 代码生成器

从 DSL（`.sql` 文件）自动生成类型安全的 Go 数据库访问代码，支持多引擎方言。

## 架构

```
DSL (.sql)  →  Parser  →  AST  →  引擎  →  生成代码
                    ↑                ↑
              sql_parser.go    engines/{pg,mssql,oracle,mysql}
```

## 安装

```bash
go install github.com/sqlgen-km/sqlgen@latest
```

## 快速开始

**1. 编写 DSL 文件 `users.sql`：**

```sql
-- package: demo

-- model: User { id int64, display_name string, gender string }

-- @根据性别和分页参数查询用户列表
-- param: gender string, page_limit int32, page_offset int32
-- name: FindByGender :many
-- model: User
SELECT id, display_name, gender
FROM users
WHERE gender = @gender
ORDER BY id DESC
LIMIT @page_limit OFFSET @page_offset
```

**2. 创建配置文件 `sqlg.yaml`：**

```yaml
tags: ["json"]
engines: ["pg"]
packages:
  - files: ["users.sql"]
```

**3. 生成代码：**

```bash
sqlgen
```

**4. 使用生成代码：**

```go
import "your-project/demo"

q, _ := demo.New(db, "postgres")
users, _ := q.FindByGender(ctx, "M", 10, 0)
```

## DSL 语法

### 文件结构

```sql
-- package: demo          # 第一行，声明 Go 包名

-- @方法文档注释            # 生成 Go doc comment
-- dsl说明注释              # 不参与生成，仅 DSL 可读

-- model: Name { field Type }   # 声明结构体
-- model: Name={col:Field,...}  # 字段映射（缺省列）
-- model: Name                  # 引用已有 model
-- model int64                  # 标量简写

-- param: name Type, ...        # 方法入参

-- name: Method :mode           # 方法名 + 执行模式
-- model: ReturnType             # 返回类型
SELECT ...                       # SQL 语句
```

### 执行模式

| `:mode` | 语义 | 返回类型 | 示例 |
|---------|------|---------|------|
| `:one` | 单行 | `(*T, error)` | `SELECT ... WHERE id = @id` |
| `:one` + `int64` | 标量 | `(int64, error)` | `SELECT COUNT(*) WHERE ...` |
| `:many` | 多行 | `([]*T, error)` | `SELECT ... ORDER BY id` |
| `:exec` | 执行 | `error` | `INSERT / UPDATE / DELETE` |
| `:execrows` | 行数 | `(int64, error)` | `DELETE WHERE status = @s` |

### model 语法

```sql
-- 声明 + 返回
-- model: User { id int64, name string, created_at time.Time }

-- 无名内联，以方法名自动命名
-- model { id int64, name string }

-- 标量
-- model int64    →  (int64, error)
-- model string   →  (string, error)

-- 字段映射：SQL列 → Go字段
-- model: OrderSummary={
    id,
    order_no,              -- 默认 id→ID, order_no→OrderNo
    total_count:Count,     -- SQL列 total_count → Go字段 Count
    status,                -- 默认 status→Status
}
```

### 参数类型

| DSL 类型 | Go 类型 | SQL 引用 |
|----------|---------|---------|
| `int`, `int64`, `int32` | 同左 | `@id` |
| `float64` | `float64` | `@score` |
| `string` | `string` | `@name` |
| `bool` | `bool` | `@active` |
| `*int64`, `*string` | 指针(可空) | `COALESCE(@x, col)` |
| `[]int64` | `[]int64` | `WHERE id IN (@ids)` |
| `time.Time` | `time.Time` | `@created_at` |
| `ModelName` | model struct | `@filter.gender` |

### 文档注释

`-- @` 开头的行生成 Go doc comment。多行支持，首行不含方法名时自动补全。

```sql
-- @根据用户ID查询完整信息
-- @包括权限和角色
-- name: FindByID :one
-- model: User
SELECT * FROM users WHERE id = @id
```

生成：

```go
// FindByID 根据用户ID查询完整信息
// 包括权限和角色
func (q *queries) FindByID(ctx context.Context, id int64) (*User, error) { ... }
```

### SQL 特性

支持 PostgreSQL 标准 SQL：

- SELECT / INSERT / UPDATE / DELETE
- 子查询、IN、EXISTS、BETWEEN
- JOIN（INNER / LEFT / RIGHT / CROSS）
- GROUP BY / HAVING / ORDER BY / LIMIT / OFFSET
- INSERT ... ON CONFLICT (DO UPDATE / DO NOTHING)
- INSERT ... RETURNING col（仅单列）
- ILIKE（其他方言自动翻译为 LOWER LIKE）
- COALESCE / NOW() 等函数

**RETURNING 限制**：仅支持 INSERT 单列 RETURNING（如 `RETURNING id`）。不支持 RETURNING *、多列 RETURNING、UPDATE/DELETE RETURNING。

## 字段映射规则

**默认**：SQL 列名（snake_case）自动映射为 Go 字段名（PascalCase）。

```sql
-- model: User { id int64, display_name string }
-- id → ID, display_name → DisplayName
```

**显式覆盖**：`{sql_col:GoField}` 仅声明不一致的列。

## 配置

在工作目录创建 `sqlg.yaml`：

```yaml
tags: ["json"]           # struct tag
engines: ["pg"]          # 目标方言（默认 ["pg"]）
packages:
  - path: "./output"     # 输出目录（默认 "."）
    files: ["sql/*.sql"]
    tags: ["json", "db"] # 局部覆盖
```

多引擎配置：

```yaml
engines: ["pg", "mssql", "oracle", "mysql"]
```

运行 `sqlgen`（无参数）即可。

## 生成代码

每个 `.sql` 文件生成：

| 文件 | 内容 |
|------|------|
| `models.go` | 所有 model struct |
| `<name>.go` | Querier 接口 + factorys map + New 函数 + 方法体 |
| `<name>.sql.pg.go` | PG 引擎 Runner 实现 |
| `<name>.sql.mssql.go` | MSSQL 引擎 Runner 实现 |
| ... | 其他引擎 |

### 生成代码结构

```go
// models.go
type User struct {
    ID          int64     `json:"id"`
    DisplayName string    `json:"display_name"`
}

// users.go
type Querier interface {
    FindByID(ctx context.Context, id int64) (*User, error)
    Close() error
}

type insertUserRunner interface {
    execReturning(ctx context.Context, name string) (int64, error)
    close() error
    withTx(tx *sql.Tx) insertUserRunner
}

type usersRunnerFactory interface {
    newInsertUser(db *sql.DB) insertUserRunner
    newFindByID(db *sql.DB) findByIdRunner
}

var factorys = map[string]usersRunnerFactory{
    "postgres": &usersRunnerFactoryPG{},
    "mysql":    &usersRunnerFactoryMySQL{},
    "oracle":   &usersRunnerFactoryOracle{},
}

func New(db *sql.DB, driver string) (Querier, error) { ... }

// users.sql.pg.go
const insertUserConstPG = `INSERT INTO users (name) VALUES ($1) RETURNING id`

type usersRunnerFactoryPG struct{}
func (f *usersRunnerFactoryPG) newInsertUser(db *sql.DB) insertUserRunner {
    return &insertUserPG{db: db}
}

type insertUserPG struct { stmt *sql.Stmt; db *sql.DB }
func (r *insertUserPG) execReturning(ctx context.Context, name string) (int64, error) {
    if r.stmt == nil { r.stmt, _ = r.db.PrepareContext(ctx, insertUserConstPG) }
    var id int64
    row := r.stmt.QueryRowContext(ctx, name)
    row.Scan(&id)
    return id, nil
}
```

**关键设计**：
- Runner 方法签名与 Querier 一致（类型化）
- 不同方言的 SQL 常量和 struct 带后缀（`ConstPG`、`findByIDPG`），可在同一包共存
- Factory map + `New(db, driver)` 自动路由方言
- 无状态 factory（空 struct），方法接收 `db *sql.DB`

## 引擎

| 引擎 | 占位符 | RETURNING | ON CONFLICT | LIMIT/OFFSET | 状态 |
|------|--------|-----------|-------------|-------------|------|
| PG | `$1, $2` | 原生 | 原生 | 原生 | ✅ |
| MySQL | `?` | 两步(Exec+LAST_INSERT_ID) | INSERT IGNORE/ON DUPLICATE | 原生 | ✅ |
| Oracle | `:1, :2` | ExecContext+sql.Out | MERGE | OFFSET/FETCH NEXT | ✅ |
| MSSQL | `@p1, @p2` | OUTPUT | MERGE | 原生 | 🚧 |

## 测试

```bash
# 全量测试
go test ./...

# 更新 golden 文件（修改输出格式后）
WRITE_GOLDEN=1 go test -run TestGolden ./...

# 集成测试（PG + MySQL + Oracle）
cd integration-test/integration && go test -count=1 -timeout 180s
```

## 文档

- [DSL 规范](docs/DSL-SPEC.md)
- [方言差异](docs/DIALECTS.md)
- [执行器设计](docs/DESIGN-RUNNER.md)
- [RETURNING 设计](docs/DESIGN-RETURNING.md)
- [引擎实现：PG](docs/engine-pg.md)
- [引擎实现：MSSQL](docs/engine-mssql.md)
- [引擎实现：Oracle](docs/engine-oracle.md)
- [引擎实现：MySQL](docs/engine-mysql.md)

## 许可

MIT
