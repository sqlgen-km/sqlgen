# sqlgen Java + MyBatis 代码生成方案

## 1. 架构

```
DSL (.sql) → Parser → AST → RunnerSpec    ← 共享层，不变
                 ↓
    ┌────────────┴────────────┐
    ↓                         ↓
languages/go/             languages/java/    ← 各自语言层
    ↓                         ↓
models.go                Item.java           ← Model
items.go                 ItemsMapper.java    ← 签名接口 (Go 对应 Querier interface)
items.sql.pg.go          ItemsMapperPG.java  ← 方言实现
items.sql.mysql.go       ItemsMapperMySQL.java
items.sql.oracle.go      ItemsMapperOracle.java
items.sql.mssql.go       ItemsMapperMSSQL.java
—                        ItemsMapperFactory.java  ← Java 独有：静态工厂
```

**原则**：语言层独立拥有自己的全套引擎实现。Go 层关注 `database/sql` 的 Prepare/QueryRow/Scan；Java 层关注 MyBatis 的 `@Select`/`@Insert`/`@SelectKey`。共享的是 AST 节点类型 + 四方言的 SQL 渲染（`$1`/`?`/`:1`/`@p1`）。

## 2. 配置

```yaml
# sqlg.yaml

engines: [pg, mysql, oracle, mssql]    # 全局方言列表，所有语言共享

go:
  tags: [json, yaml]
  packages:
    - out: "internal/infrastructure/persistence/sqlgen"
      files: ["sqlgen/*.sql"]

java:
  packages:
    - modelPackage:  "com.example.hospital.entity"
      mapperPackage: "com.example.hospital.mapper"
      out: "src/main/java"
      files: ["sqlgen/*.sql"]
```

字段说明：

| 字段 | 说明 |
|------|------|
| `engines` | 全局方言列表（`pg`/`mysql`/`oracle`/`mssql`） |
| `go` / `java` | 语言块，不配则不生成 |
| `go.tags` | Go 专有：struct tag |
| `go.packages[].out` | Go 输出目录 |
| `java.packages[].modelPackage` | Java 专有：Model 的包名 |
| `java.packages[].mapperPackage` | Java 专有：Mapper 的包名 |
| `java.packages[].out` | Java 输出根目录 |

## 3. 目录结构变更

```
sqlgen/
├── ast/                          ← 共享层（不变）
│   ├── ast.go
│   ├── render.go
│   └── runtime.go
├── parser.go / sql_parser.go     ← 共享层（不变）
├── main.go / config.go           ← 适配新配置
│
└── languages/
    ├── go/                       ← 现有 engine/ + generator.go 迁入
    │   ├── engine.go
    │   ├── generator.go
    │   ├── pg/pg.go
    │   ├── mysql/mysql.go
    │   ├── oracle/oracle.go
    │   └── mssql/mssql.go
    │
    └── java/                     ← 新增
        ├── engine.go             # Java Engine 接口
        ├── generator.go          # 代码生成入口
        ├── model.go              # Model Record 生成
        ├── mapper.go             # Mapper 接口生成（5 种 RunnerKind）
        ├── factory.go            # Factory 类生成
        ├── render.go             # 方言 SQL → #{param} 占位替换
        ├── pg/pg.go              # Java + PG
        ├── mysql/mysql.go        # Java + MySQL
        ├── oracle/oracle.go      # Java + Oracle
        └── mssql/mssql.go        # Java + MSSQL
```

## 4. Java Engine 接口

```go
// languages/java/engine.go

type Engine interface {
    // Name returns the engine identifier ("pg", "mysql", "oracle", "mssql").
    Name() string

    // Profile returns the Spring profile name ("pg", "mysql", "oracle", "mssql").
    Profile() string

    // DriverName returns the JDBC driver name for the factory switch:
    // "postgresql", "mysql", "oracle", "sqlserver".
    DriverName() string

    // GenMapper generates method bodies for the Mapper implementation interface.
    // Returns the full interface body (methods annotated with @Select/@Insert/...).
    GenMapper(stem string, specs []RunnerSpec) string
}
```

## 5. 五种 RunnerKind → MyBatis 注解

| RunnerKind | 注解 | Java 返回类型 | 备注 |
|---|---|---|---|
| `RunnerQueryOne` | `@Select` | `T` | 找不到返回 null |
| `RunnerQueryOne` 标量 | `@Select` | `long` / `String` | |
| `RunnerQueryMany` | `@Select` | `List<T>` | |
| `RunnerQueryMany` 标量 | `@Select` | `List<Long>` | |
| `RunnerExec` | `@Insert` / `@Update` / `@Delete` | `void` | |
| `RunnerExecRows` | `@Insert` / `@Update` / `@Delete` | `long` | affected rows |
| `RunnerReturningScalar` | 见 §6 | `long`（item.keyProperty） | ID 写入参数的 keyProperty |

## 6. INSERT RETURNING — 各数据库策略

| 数据库 | MyBatis 注解 | SQL 文本 |
|--------|------------|---------|
| PG | `@Insert` + `@Options(useGeneratedKeys=true, keyProperty="id", keyColumn="id")` | `INSERT ... RETURNING id` |
| MySQL | `@Insert` + `@SelectKey(statement="SELECT LAST_INSERT_ID()", keyProperty="id", before=false, resultType=long.class)` | `INSERT ...` |
| Oracle | `@Insert` + `@SelectKey(statement="SELECT seq.NEXTVAL FROM dual", keyProperty="id", before=true, resultType=long.class)` | `INSERT INTO t (id, ...) VALUES (#{id}, ...)` |
| MSSQL | `@Insert` + `@Options(useGeneratedKeys=true, keyProperty="id", keyColumn="id")` | `INSERT ... OUTPUT INSERTED.id` |

## 7. ON CONFLICT — 各数据库 SQL 文本

| 数据库 | DO NOTHING | DO UPDATE |
|--------|-----------|-----------|
| PG | `ON CONFLICT (...) DO NOTHING` | `ON CONFLICT (...) DO UPDATE SET ...` |
| MySQL | `INSERT IGNORE INTO ...` | `... ON DUPLICATE KEY UPDATE col = VALUES(col)` |
| Oracle | `MERGE INTO ... WHEN NOT MATCHED THEN INSERT ...` | `MERGE INTO ... WHEN MATCHED THEN UPDATE ...` |
| MSSQL | `MERGE INTO ... WHEN NOT MATCHED THEN INSERT ...` | `MERGE INTO ... WHEN MATCHED THEN UPDATE ...` |

## 8. SQL 占位符转换

引擎层通过 `ast.Dialect.Render()` 产出方言 SQL（`$1`/`?`/`:1`/`@p1`），Java 层的 `render.go` 做后处理：

```go
func renderMyBatisSQL(spec RunnerSpec, dialectSQL string) string {
    for _, p := range spec.Params {
        dialectSQL = replaceFirstPlaceholder(dialectSQL, "#{"+p.Name+"}")
    }
    return dialectSQL
}
```

## 9. 类型映射

| DSL 类型 | Java 类型 |
|----------|-----------|
| `int64` | `long` |
| `int32` | `int` |
| `float64` | `java.math.BigDecimal` |
| `string` | `String` |
| `bool` | `boolean` |
| `time.Time` | `java.time.LocalDateTime` |
| `*string` | `String`（MyBatis 自动处理 null → setNull） |
| `*int64` | `Long`（boxed，可为 null） |
| `*time.Time` | `LocalDateTime`（MyBatis 自动处理 null） |
| `[]byte` | `byte[]` |
| `json.RawMessage` | `String` |

## 10. 生成文件详细规范

### 10.1 Item.java — Model Record

```java
// Code generated by sqlgen; DO NOT EDIT.
package com.example.hospital.entity;

import java.math.BigDecimal;
import java.time.LocalDateTime;

public record Item(
    long id,
    String name,
    String category,
    BigDecimal price,
    int stock,
    LocalDateTime createdAt,
    LocalDateTime updatedAt
) {}
```

### 10.2 ItemsMapper.java — 共享签名接口

不含任何 MyBatis 注解，只有方法签名。对应 Go 的 Querier interface。

```java
// Code generated by sqlgen; DO NOT EDIT.
package com.example.hospital.mapper;

import java.util.List;
import com.example.hospital.entity.Item;
import org.apache.ibatis.annotations.Param;

public interface ItemsMapper {

    Item findByID(@Param("id") long id);
    List<Item> findAll();
    void insertItem(@Param("name") String name, @Param("category") String category);
    long insertAndReturnID(Item item);
    long updateItem(@Param("name") String name, @Param("id") long id);
    long deleteItem(@Param("id") long id);
}
```

### 10.3 ItemsMapperPG.java — PG 实现

```java
// Code generated by sqlgen; DO NOT EDIT.
package com.example.hospital.mapper;

import java.math.BigDecimal;
import java.util.List;
import com.example.hospital.entity.Item;
import org.apache.ibatis.annotations.*;
import org.springframework.context.annotation.Profile;

@Mapper
@Profile("pg")
public interface ItemsMapperPG extends ItemsMapper {

    @Override
    @Select("SELECT id, name, category, price, stock, created_at, updated_at " +
            "FROM items WHERE id = #{id}")
    Item findByID(@Param("id") long id);

    @Override
    @Insert("INSERT INTO items (name, category) VALUES (#{name}, #{category}) RETURNING id")
    @Options(useGeneratedKeys = true, keyProperty = "id", keyColumn = "id")
    long insertAndReturnID(Item item);
}
```

### 10.4 ItemsMapperMySQL.java — MySQL 实现

```java
@Mapper
@Profile("mysql")
public interface ItemsMapperMySQL extends ItemsMapper {

    @Override
    @Insert("INSERT INTO items (name, category) VALUES (#{name}, #{category})")
    @SelectKey(statement = "SELECT LAST_INSERT_ID()",
               keyProperty = "id", before = false, resultType = long.class)
    long insertAndReturnID(Item item);
}
```

### 10.5 ItemsMapperFactory.java — 静态工厂

```java
// Code generated by sqlgen; DO NOT EDIT.
package com.example.hospital.mapper;

import org.apache.ibatis.session.SqlSession;

public class ItemsMapperFactory {

    /**
     * Create ItemsMapper for the given database driver.
     * Valid driver names: postgresql, mysql, oracle, sqlserver.
     */
    public static ItemsMapper create(SqlSession session, String driver) {
        return switch (driver) {
            case "postgresql" -> session.getMapper(ItemsMapperPG.class);
            case "mysql"      -> session.getMapper(ItemsMapperMySQL.class);
            case "oracle"     -> session.getMapper(ItemsMapperOracle.class);
            case "sqlserver"  -> session.getMapper(ItemsMapperMSSQL.class);
            default -> throw new IllegalArgumentException(
                "Unsupported driver: " + driver);
        };
    }
}
```

## 11. 业务代码使用

### Spring 模式

```java
@SpringBootApplication
@MapperScan("com.example.hospital.mapper")
public class Application {
    public static void main(String[] args) {
        SpringApplication.run(Application.class, args);
    }
}

@Service
public class ItemService {
    @Autowired
    private ItemsMapper mapper;

    public Item getItem(long id) {
        return mapper.findByID(id);
    }
}
```

```bash
java -Dspring.profiles.active=mysql    -jar app.jar
java -Dspring.profiles.active=oracle   -jar app.jar
```

### 非 Spring 模式

```java
SqlSession session = sqlSessionFactory.openSession();
ItemsMapper mapper = ItemsMapperFactory.create(session, "postgresql");
Item item = mapper.findByID(1);
```

## 12. 产出文件清单

```
src/main/java/com/example/hospital/
├── entity/
│   └── Item.java                  ← Model Record
└── mapper/
    ├── ItemsMapper.java           ← 共享签名
    ├── ItemsMapperPG.java         ← @Mapper @Profile("pg")
    ├── ItemsMapperMySQL.java      ← @Mapper @Profile("mysql")
    ├── ItemsMapperOracle.java     ← @Mapper @Profile("oracle")
    ├── ItemsMapperMSSQL.java      ← @Mapper @Profile("mssql")
    └── ItemsMapperFactory.java    ← 静态工厂
```

## 13. 实现计划

| Phase | 内容 |
|-------|------|
| 1 | 重构：现有 `engines/` + `generator.go` 迁入 `languages/go/`，拆出 Go 语言层 |
| 2 | `config.go` / `main.go` 适配新配置格式 |
| 3 | `languages/java/engine.go` — Java Engine 接口 |
| 4 | `languages/java/pg/pg.go` — PG 引擎实现 |
| 5 | `languages/java/mysql/mysql.go` — MySQL 引擎实现 |
| 6 | `languages/java/oracle/oracle.go` — Oracle 引擎实现 |
| 7 | `languages/java/mssql/mssql.go` — MSSQL 引擎实现 |
| 8 | `languages/java/generator.go` + `model.go` + `mapper.go` + `factory.go` + `render.go` |
| 9 | `main.go` — 遍历 language × engine 分发 |
| 10 | Golden 测试 + 集成测试 |
