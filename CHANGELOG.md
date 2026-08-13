# Changelog

## v1.2.2 (未发布)

数组参数跨方言支持。

### 新增

- **数组成员语法 `= ANY(@arr)`** — 数组参数（`[]int64`/`[]string`）成员判断唯一写法；`IN (@x)` 单参数生成时报错
- **四方言特化渲染** — PG 原生 `= ANY($1)`、MySQL `JSON_CONTAINS`、Oracle `TABLE()` 反嵌套、MSSQL `OPENJSON`
- **Java TypeHandler 生成** — 每个（方言 × 元素类型）生成 TypeHandler；SQL 内联 `#{param, typeHandler=FQN}` 写参 + `@Results` 读回
- **Go 数组绑定** — PG `pq.Array`、MySQL/MSSQL JSON 序列化、Oracle `go_ora.Object`
- **空数组语义** — 空数组 = 返回空结果（不报错）

### 修复

- **Oracle 数组成员用 `TABLE()` 替代 `MEMBER OF`** — `SYS.ODCINUMBERLIST`/`ODCIVARCHAR2LIST` 是 VARRAY，`MEMBER OF` 只认嵌套表（ORA-00932）
- **Go Oracle 空数组短路** — go-ora 绑空集合 ORA-00600，框架方法对空数组短路

## v1.2.1 (2026-08-12)

INSERT RETURNING 对象参数化 + Java Mapper 类型修复。

### 变更

- **INSERT RETURNING 改对象参数 + void 返回** — 入参扁平化为 `InsertXxxParams`，ID 经 keyProperty 注入参数对象，方法返回 `void`
- **MySQL/Oracle `@SelectKey` → `@Options(useGeneratedKeys)`** — 与 PG 统一；Oracle 用 `@SelectKey(before=true)` + 序列注入 `#{id}`
- **Java Mapper 方法签名类型修复** — `writeMethod` 改用 `spec.ModelType` 选择返回/参数类型（修复「System 类型」bug）
- **`{stem}` 强制追加到 model/mapper 包** + query 名查重

## v1.2.0 (2026-08-11)

Java/MyBatis 代码生成支持。

### 新增

- **Java/MyBatis 代码生成** — DSL 可以生成 Java 代码（Model Record + Mapper 接口 + Factory）
- **多引擎 Mapper** — 每个 SQL 方言生成独立 Mapper 实现（`ItemsMapperPG`、`ItemsMapperMySQL`、`ItemsMapperOracle`、`ItemsMapperMSSQL`）
- **Spring 集成** — Mapper 生成 `@Mapper`、`@Profile` 注解，Factory 的静态 `create()` 方法可在无 Spring 环境使用
- **engineSubPackage 配置** — Java 新增 `engineSubPackage` 选项，开启后引擎实现类进入 `mapperPackage.{engine}` 子包（如 `com.dc.mapper.pg.ItemsMapperPG`），减少单目录文件数
- **{stem} 占位符** — `mapperPackage` 支持 `{stem}` 按 DSL 文件名自动分组
- **ON CONFLICT 支持** — INSERT 语句的 `ON CONFLICT DO NOTHING/UPDATE` 跨方言翻译（PG 原生、MySQL `INSERT IGNORE`/`ON DUPLICATE KEY`、Oracle PL/SQL、MSSQL MERGE）
- **Golden 测试** — Java 生成输出增加 golden 文件测试

### 变更

- **代码组织重构** — Go/Java 引擎从 `engines/` 移入 `languages/go/` 和 `languages/java/`
- **meta 包抽取** — 共享类型（`ParsedFile`、`ModelDef`、`QueryDef`）和查询构建逻辑独立为 `meta/` 包
- **配置类型归位** — `GoPkgCfg` 移入 `languages/golang/`，`JavaPkgCfg` 移入 `languages/java/`，删除 `config.go` 中的 alias

## v1.0.1 (2026-08-11)

Oracle 引擎兼容性修复。

### 修复

- **Oracle：小写 `true`/`false` 字面量** — `renderSQL` 中 `FALSE→0` 替换只匹配大写，导致 DSL 中的小写 `false` 直接写入 SQL，Oracle 报 `ORA-00984: column not allowed`。新增小写版本 `true→1`、`false→0`。
- **Oracle：GROUP BY 改写遗漏 WHERE 子句** — `rewriteGroupByToSubquery` 将 `LEFT JOIN + GROUP BY` 转为标量子查询时，只处理 SELECT，漏了 WHERE 中引用 JOIN 表别名的条件，导致 `ORA-00904: "TM"."USER_ID"`。修复后将 `tm.user_id = :N` 改写为 `EXISTS (SELECT 1 FROM teams_member tm WHERE tm.team_id = t.id AND tm.user_id = :N)`。
- **Oracle：DO NOTHING 用 PL/SQL 替代 MERGE** — go-ora 驱动执行 MERGE 不报错也不插入数据。`ON CONFLICT DO NOTHING` 的 Oracle 翻译从 MERGE 改为 PL/SQL `BEGIN INSERT ... EXCEPTION WHEN DUP_VAL_ON_INDEX THEN NULL; END;`。

## v1.0.0 (2026-08-08)

首个正式发布。

### 概述

sqlgen 是一个多方言 SQL 代码生成器。从 DSL（`.sql` 文件）自动生成类型安全的 Go 数据库访问代码，支持 PostgreSQL、MySQL、Oracle 和 SQL Server。

### 核心特性

- **多引擎支持**：PG、MySQL、Oracle、MSSQL 四方言，一份 DSL 生成所有引擎代码
- **方言后缀**：不同方言的 Runner 和 SQL 常量带后缀（`ConstPG`、`findByIDOracle`），可在同一包共存
- **类型化方法签名**：Runner 方法参数与 Querier 接口一致，不使用 `args ...any`
- **统一构造函数**：`New(db *sql.DB, driver string)` 通过 factory map 自动路由方言
- **无状态 Factory**：Factory 为空 struct，方法接收 `db *sql.DB`，零分配
- **Lazy Stmt**：`*sql.Stmt` 首次调用时 Prepare，并发安全
- **SELECT \\* 替换**：`SELECT *` 自动展开为 model 的显式列名
- **多余列丢弃**：SELECT 列多于 model 字段时自动扫描到临时变量丢弃
- **ILIKI 跨方言**：PG 保留原生 ILIKE，其他引擎翻译为 `LOWER LIKE LOWER`
- **可选参数**：`*string` / `*int64` 等指针类型支持 `OR @param IS NULL` 模式

### RETURNING

- 仅支持 INSERT 单列 RETURNING（如 `RETURNING id`）
- MySQL：两步执行（Exec → SELECT LAST_INSERT_ID）
- Oracle：ExecContext + sql.Out 绑定
- 不支持的 RETURNING 模式在生成时报错

### 引擎详情

| | PG | MySQL | Oracle | MSSQL |
|---|---|---|---|---|
| 占位符 | `$1,$2` | `?` | `:1,:2` | `@p1,@p2` |
| RETURNING | 原生 | 两步 | sql.Out | OUTPUT |
| ON CONFLICT | 原生 | INSERT IGNORE | PL/SQL | MERGE |
| LIMIT/OFFSET | 原生 | 原生 | FETCH NEXT | 原生 |
| 状态 | ✅ | ✅ | ✅ | 🚧 |

### 测试

- 17 个集成测试场景 × PG/MySQL/Oracle = 51 子测试
- 4 个 RETURNING 错误测试
- 10+ Golden 测试覆盖全部 DSL 模式

### 安装

```bash
go install github.com/sqlgen-km/sqlgen@v1.0.1
```
