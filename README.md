# sqlgen — SQL 代码生成器

从 DSL（`.sql` 文件）自动生成类型安全的数据库访问代码，支持 Go 和 Java/MyBatis 双语言输出，一份 DSL 生成四种方言。

## 架构

```
DSL (.sql)  →  Parser  →  AST  →  meta  →  languages/{go,java}  →  生成代码
```

## 安装

```bash
go install github.com/sqlgen-km/sqlgen@latest
```

## 快速开始

### Go

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
engines: ["pg", "mysql", "oracle", "mssql"]
go:
  tags: ["json"]
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

### Java

**1. DSL 文件同上。**

**2. 创建配置文件 `sqlg.yaml`：**

```yaml
engines: ["pg", "mysql"]
java:
  packages:
    - files: ["users.sql"]
      modelPackage: "com.example.entity"
      mapperPackage: "com.example.mapper"
```

**3. 生成代码，输出到 `mapperPackage` 对应目录。**

**4. 使用生成代码（Spring Boot）：**

```java
@Autowired
private SqlSessionFactory sqlSessionFactory;

SqlSession session = sqlSessionFactory.openSession();
UsersMapper mapper = UsersMapperFactory.create(session, "postgresql");
User user = mapper.findByID(1);
```

## 配置

在工作目录创建 `sqlg.yaml`：

```yaml
# 目标方言（默认 ["pg"]）
engines: ["pg", "mysql", "oracle", "mssql"]

# ── Go 语言配置 ──
go:
  tags: ["json"]           # struct tag（可被 packages 级别覆盖）
  packages:
    - out: "./output"      # 输出目录（默认 "."）
      files: ["sql/*.sql"]
      tags: ["json", "db"] # 局部覆盖

# ── Java 语言配置 ──
java:
  packages:
    - files: ["sql/*.sql"]
      modelPackage: "com.example.entity"     # Model Record 包名
      mapperPackage: "com.example.mapper"    # Mapper 接口包名
      out: "./src/main/java"                 # 输出根目录（默认 "."）
      engineSubPackage: true                 # 引擎 Mapper 放子包（默认 false）
```

### Java 配置说明

| 配置项 | 说明 |
|--------|------|
| `modelPackage` | Model Record 的 Java 包名 |
| `mapperPackage` | Mapper 接口 + Factory 的 Java 包名，支持 `{stem}` 占位符按 DSL 文件名分组 |
| `out` | 输出根目录，包路径从 `out` 开始拼接 |
| `engineSubPackage` | 开启后引擎 Mapper 进入 `mapperPackage.{engine}` 子包（如 `com.example.mapper.pg.UsersMapperPG`），减少单目录文件数 |

## DSL 语法

### 文件结构

```sql
-- package: demo          # 第一行，声明包名（Go）或 Java 全限定包名

-- @方法文档注释            # 生成 Go doc comment / Java Javadoc
-- dsl说明注释              # 不参与生成，仅 DSL 可读

-- model: Name { field Type }   # 声明结构体/Record
-- model: Name={col:Field,...}  # 字段映射（缺省列）
-- model: Name                  # 引用已有 model
-- model int64                  # 标量简写

-- param: name Type, ...        # 方法入参

-- name: Method :mode           # 方法名 + 执行模式
-- model: ReturnType             # 返回类型
SELECT ...                       # SQL 语句
```

### 执行模式

| `:mode` | 语义 | Go 返回类型 | Java 返回类型 | 示例 |
|---------|------|------------|-------------|------|
| `:one` | 单行 | `(*T, error)` | `T` | `SELECT ... WHERE id = @id` |
| `:one` + `int64` | 标量 | `(int64, error)` | `long` | `SELECT COUNT(*) WHERE ...` |
| `:many` | 多行 | `([]*T, error)` | `List<T>` | `SELECT ... ORDER BY id` |
| `:exec` | 执行 | `error` | `void` | `INSERT / UPDATE / DELETE` |
| `:execrows` | 行数 | `(int64, error)` | `long` | `DELETE WHERE status = @s` |

### model 语法

```sql
-- 声明 + 返回
-- model: User { id int64, name string, created_at time.Time }

-- 无名内联，以方法名自动命名
-- model { id int64, name string }

-- 标量
-- model int64    →  Go: (int64, error)  /  Java: long
-- model string   →  Go: (string, error) /  Java: String

-- 字段映射：SQL列 → Go字段/Java字段
-- model: OrderSummary={
    id,
    order_no,              -- 默认 id→id/ID, order_no→orderNo/OrderNo
    total_count:Count,     -- SQL列 total_count → 字段 Count/count
    status,                -- 默认 status→Status/status
}
```

### 参数类型

| DSL 类型 | Go 类型 | Java 类型 | SQL 引用 |
|----------|---------|----------|---------|
| `int`, `int64`, `int32` | 同左 | `long`, `int` | `@id` |
| `float64` | `float64` | `java.math.BigDecimal` | `@score` |
| `string` | `string` | `String` | `@name` |
| `bool` | `bool` | `boolean` | `@active` |
| `*int64`, `*string` | 指针(可空) | 包装类型 | `COALESCE(@x, col)` |
| `[]int64` | `[]int64` | `List<Long>` | `WHERE id IN (@ids)` |
| `time.Time` | `time.Time` | `java.time.Instant` | `@created_at` |
| `ModelName` | model struct | model Record | `@filter.gender` |

### 文档注释

`-- @` 开头的行生成文档注释。多行支持，首行不含方法名时自动补全。

```sql
-- @根据用户ID查询完整信息
-- @包括权限和角色
-- name: FindByID :one
-- model: User
SELECT * FROM users WHERE id = @id
```

Go 生成：

```go
// FindByID 根据用户ID查询完整信息
// 包括权限和角色
func (q *queries) FindByID(ctx context.Context, id int64) (*User, error) { ... }
```

### SQL 特性

- SELECT / INSERT / UPDATE / DELETE
- 子查询、IN、EXISTS、BETWEEN
- JOIN（INNER / LEFT / RIGHT / CROSS）
- GROUP BY / HAVING / ORDER BY / LIMIT / OFFSET
- INSERT ... ON CONFLICT (DO UPDATE / DO NOTHING) — 跨方言翻译
- INSERT ... RETURNING col（仅单列）
- ILIKE（其他方言自动翻译为 LOWER LIKE）
- COALESCE / NOW() 等函数

**RETURNING 限制**：仅支持 INSERT 单列 RETURNING（如 `RETURNING id`）。不支持 RETURNING *、多列 RETURNING、UPDATE/DELETE RETURNING。

## 字段映射规则

**默认**：SQL 列名（snake_case）自动映射为字段名（Go PascalCase / Java camelCase）。

```sql
-- model: User { id int64, display_name string }
-- Go:  id → ID, display_name → DisplayName
-- Java: id → id, display_name → displayName
```

**显式覆盖**：`{sql_col:GoField}` 仅声明不一致的列。

## 生成代码

### Go 输出

每个 package 生成：

| 文件 | 内容 |
|------|------|
| `models.go` | 所有 model struct |
| `<name>.go` | Querier 接口 + factorys map + New 函数 + 方法体 |
| `<name>.sql.pg.go` | PG 引擎 Runner 实现 |
| `<name>.sql.mysql.go` | MySQL 引擎 Runner 实现 |
| `<name>.sql.oracle.go` | Oracle 引擎 Runner 实现 |
| `<name>.sql.mssql.go` | MSSQL 引擎 Runner 实现 |

### Java 输出

每个 DSL 文件生成：

| 文件 | 内容 |
|------|------|
| `<Model>.java` | Java Record（modelPackage） |
| `<Name>Mapper.java` | Mapper 接口 + `@Mapper` 注解 |
| `<Name>MapperPG.java` | PG 方言 Mapper 实现（`@Profile("postgresql")`） |
| `<Name>MapperMySQL.java` | MySQL 方言 Mapper 实现 |
| `<Name>MapperOracle.java` | Oracle 方言 Mapper 实现 |
| `<Name>MapperMSSQL.java` | MSSQL 方言 Mapper 实现 |
| `<Name>MapperFactory.java` | Factory 类，静态 `create()` 驱动路由 |

**关键设计**：
- Mapper 接口为抽象契约，所有方言实现同一接口
- 方言 Mapper 用 `@Profile` 注解自动激活对应数据源
- Factory 的静态 `create()` 方法可在无 Spring 环境手动路由
- `engineSubPackage: true` 时引擎 Mapper 分散到子包，减少单目录文件数

## 引擎

| 引擎 | 占位符 | RETURNING | ON CONFLICT | LIMIT/OFFSET | 状态 |
|------|--------|-----------|-------------|-------------|------|
| PG | `$1, $2` | 原生 | 原生 | 原生 | ✅ |
| MySQL | `?` | 两步(Exec+LAST_INSERT_ID) | INSERT IGNORE / ON DUPLICATE | 原生 | ✅ |
| Oracle | `:1, :2` | ExecContext+sql.Out | PL/SQL 异常处理 | OFFSET/FETCH NEXT | ✅ |
| MSSQL | `@p1, @p2` | OUTPUT | MERGE | 原生 | ✅ |

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
