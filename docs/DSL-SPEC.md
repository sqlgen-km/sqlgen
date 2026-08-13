# sqlgen DSL 规范

## 1. 文件结构

```sql
-- package: pkgname

-- @注释行（生成 Go doc comment）
-- dsl 说明注释（不参与生成）

-- model: Name { field Type, ... }
-- model: Name={alias1:field1, alias2:field2}
-- model: Name
-- model scalar

-- param: name Type, ...

-- name: MethodName :mode
-- model: ReturnType
SQL 语句;
```

指令间用空行分隔作用域。

### 1.1 文档注释 `-- @`

`-- @` 开头的行生成 Go doc comment。`--` 无 `@` 且非指令的行仅作为 DSL 说明，不参与生成。

**规则**：如果第一行不以 `<methodName>` 开头，生成时自动在首行前补 `methodName`。

```
-- @根据用户ID查询用户信息
-- @返回完整资料
-- name: FindByID :one
-- model: User
SELECT id, display_name FROM users WHERE id = @id
```

生成：
```go
// FindByID 根据用户ID查询用户信息
// 返回完整资料
func (q *queries) FindByID(ctx context.Context, id int64) (*User, error) {
```

## 2. 字段映射规则

### 2.1 默认映射

SQL 列名（snake_case）→ Go 字段名（PascalCase），自动转换。

```sql
-- model: User {
    id           int64,
    display_name string,
    created_at   time.Time
}
```

| SQL 列 | Go 字段 |
|--------|---------|
| `id` | `ID` |
| `display_name` | `DisplayName` |
| `created_at` | `CreatedAt` |

### 2.2 显式映射 `:`

仅当 SQL 列名与 Go 字段名不一致时，用 `sql_col:GoField` 声明。未声明的列按默认规则处理。

```sql
-- model: OrderSummary={
    id,
    order_no,
    owner_name:Owner,
    total_count:Count,
    created_at:Created
}
```

| SQL 列 | Go 字段 | 说明 |
|--------|---------|------|
| `id` | `ID` | 默认 |
| `order_no` | `OrderNo` | 默认 |
| `owner_name` | `Owner` | 显式 |
| `total_count` | `Count` | 显式 |
| `created_at` | `Created` | 显式 |

### 2.3 SELECT *

`SELECT *` 在生成时自动替换为 model 的显式列名。

```sql
-- model: User { id int64, name string, email string }
-- name: GetUser :one
-- model: User
SELECT * FROM users WHERE id = @id
```

生成 SQL：`SELECT id, name, email FROM users WHERE id = $1`

### 2.4 多余列丢弃

SELECT 的列数可以多于 model 字段数。多余的列会扫描到临时变量 `_dN` 并丢弃，不报错也不编译失败。

```sql
-- model: Brief { id int64, name string }
-- name: FindBrief :one
-- model: Brief
SELECT id, name, price, created_at FROM items WHERE id = @id
```

生成：
```go
var _d1 interface{}
var _d2 interface{}
row.Scan(&r.ID, &r.Name, &_d1, &_d2)
```


## 3. 入参 → 方法签名

### 3.1 签名规则

```
-- param: name Type, ...
-- name: MethodName :mode
-- model: ReturnType

→ func (q *queries) MethodName(ctx context.Context, name Type, ...) (ReturnType, error)
```

### 3.2 类型映射

| DSL 类型 | Go 类型 |
|----------|---------|
| `int`, `int64`, `int32` | 同左 |
| `float64` | `float64` |
| `string` | `string` |
| `bool` | `bool` |
| `time.Time` | `time.Time` |
| `[]int64`, `[]string` | 同左 |
| `*string`, `*int64`, `*bool` | 同左 |
| `ModelName` | model struct |

### 3.3 执行模式

| `:mode` | 语义 | 返回类型 |
|---------|------|---------|
| `:one` | 单行 | `(*T, error)` |
| `:one` + 标量 | 单值 | `(T, error)` |
| `:many` | 多行 | `([]*T, error)` |
| `:exec` | 执行 | `error` |
| `:execrows` | 影响行数 | `(int64, error)` |

### 3.4 返回类型映射

| `-- model:` | `:one` 返回 | `:many` 返回 |
|------------|-----------|-----------|
| `: User` | `(*User, error)` | `([]*User, error)` |
| `int64` | `(int64, error)` | `([]int64, error)` |
| `string` | `(string, error)` | `([]string, error)` |
| 无名 `{ fields }` | `(*MethodName, error)` | `([]*MethodName, error)` |

### 3.5 示例

```
-- param: filter Filter, limit int32, offset int32
-- name: SearchUsers :many
-- model: User
→ func (q *queries) SearchUsers(ctx, Filter, int32, int32) ([]*User, error)

-- param: id int64
-- name: FindByID :one
-- model: User
→ func (q *queries) FindByID(ctx, id int64) (*User, error)

-- param: name string
-- name: InsertUser :one
-- model int64
INSERT INTO users (name) VALUES (@name) RETURNING id
→ func (q *queries) InsertUser(ctx, name string) (int64, error)

-- param: keyword *string, limit int32
-- name: SearchByName :many
-- model: User
→ func (q *queries) SearchByName(ctx, keyword *string, limit int32) ([]*User, error)
```

## 4. SQL 特性

### 4.1 支持

- SELECT / INSERT / UPDATE / DELETE
- 子查询、IN、EXISTS、BETWEEN
- 数组成员 `= ANY(@arr)`（数组参数唯一写法，见 §4.4）
- JOIN（INNER / LEFT / RIGHT / CROSS）
- GROUP BY / HAVING / ORDER BY / LIMIT / OFFSET
- INSERT ... ON CONFLICT (DO UPDATE / DO NOTHING)
- INSERT ... RETURNING col（仅单列）
- ILIKE（非 PG 方言自动翻译为 LOWER LIKE）
- COALESCE / NOW() 等函数
- `*string` / `*int64` 等可空参数（`OR @param IS NULL` 模式）

### 4.2 RETURNING 限制

仅支持 INSERT 单列 RETURNING。以下模式生成时报错：

| 不支持 | 错误信息 |
|--------|---------|
| `RETURNING *` | `RETURNING * not supported` |
| `RETURNING col1, col2` | `multi-column RETURNING not supported` |
| `UPDATE ... RETURNING` | `UPDATE RETURNING not supported` |
| `DELETE ... RETURNING` | `DELETE RETURNING not supported` |

### 4.3 不支持的特性

| 特性 | 替代 |
|------|------|
| `::` 类型转换 | `CAST(x AS type)` |
| `CASE WHEN @param` | 应用层 |
| `UNION` | `LEFT JOIN + OR` |

### 4.4 数组成员 `= ANY(@arr)`

数组参数（`[]int64` / `[]string`）的成员判断用 `= ANY(@arr)`，是唯一规范写法：

```sql
-- param: group_ids []int64
-- name: FindGroupNamesByIDs :many
-- model: Group
SELECT id, name FROM groups WHERE id = ANY(@group_ids)
```

四方言各自翻译（**签名统一，实现各自特化**）：

| 方言 | 渲染 |
|------|------|
| PG | `id = ANY($1)` |
| MySQL | `JSON_CONTAINS(?, CAST(id AS JSON))` |
| Oracle | `id IN (SELECT COLUMN_VALUE FROM TABLE(:1))` |
| MSSQL | `id IN (SELECT value FROM OPENJSON(@p1))` |

**IN 子句约束**：

- `IN (@x)`（括号内仅一个参数，无论是否数组）→ **生成时报错**，提示改用 `= ANY(@x)`（数组）或 `= @x`（标量）。
- `IN (1, 2, 3)` 字面量列表、`IN (@a, @b)` 多参数、`IN (SELECT ...)` 子查询 → 正常保留。
- `IN` 语法不用于处理数组参数。

空数组语义 = **返回空结果**（不报错）。详见 `docs/design/array-params.md`。

## 5. 使用方式

```go
import "your-project/demo"

db, _ := sql.Open("postgres", dsn)
q, _ := demo.New(db, "postgres")
defer q.Close()

user, _ := q.FindByID(ctx, 1)
users, _ := q.FindByGender(ctx, "M", 10, 0)
```

支持的 driver 名：

| driver | 方言 |
|--------|------|
| `"postgres"` | PG |
| `"mysql"` | MySQL |
| `"oracle"` | Oracle |
| `"sqlserver"` | MSSQL |
