# Changelog

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
