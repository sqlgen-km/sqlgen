# Changelog

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
- **SELECT \* 替换**：`SELECT *` 自动展开为 model 的显式列名
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
| ON CONFLICT | 原生 | INSERT IGNORE | MERGE | MERGE |
| LIMIT/OFFSET | 原生 | 原生 | FETCH NEXT | 原生 |
| 状态 | ✅ | ✅ | ✅ | 🚧 |

### 测试

- 17 个集成测试场景 × PG/MySQL/Oracle = 51 子测试
- 4 个 RETURNING 错误测试
- 10+ Golden 测试覆盖全部 DSL 模式

### 安装

```bash
go install github.com/sqlgen-km/sqlgen@v1.0.0
```
