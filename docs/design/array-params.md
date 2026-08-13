# 数组参数跨方言支持设计

**日期**: 2026-08-13
**状态**: 方案已定稿（待实现）
**影响**: Java 引擎 + Go 引擎，四方言（PG/MySQL/Oracle/MSSQL）
**关联 bug**: `/work/weichuang/sqlgen-bug/04-java-array-param-no-typehandler.md`

## 背景

DSL 中数组参数（`[]int64`/`[]string`）当前被翻译成 Java 数组（`long[]`/`String[]`）后用
`#{...}` 直绑。MyBatis 只认 `byte[]`，其余数组无默认 TypeHandler，mapper 注册阶段即崩
（`Type handler was null`）。Go 侧同样问题：`IN (?)` + `[]int64` 裸传，除 PG（靠驱动
特性）外全挂。

## 原则

1. **`= ANY(@arr)` 是 DSL 语法糖**，表达「数组成员」语义。
2. **四方言签名统一**（参数类型一致），**实现各自特化**（SQL + 绑定各自实现，不强求一致）。
3. **PG 走原生** `= ANY($1)`；MySQL/Oracle/MSSQL 各自特化（MySQL `JSON_CONTAINS`、Oracle `TABLE()` 反嵌套、
   MSSQL `OPENJSON`）。MSSQL 从 §14 的 `STRING_SPLIT` 改为 `OPENJSON`（空数组正确性，见「空数组语义」）。
   Oracle 从 `MEMBER OF` 改为 `TABLE()`：`SYS.ODCINUMBERLIST`/`ODCIVARCHAR2LIST` 是 **VARRAY**，
   `MEMBER OF` 只认嵌套表，实测 `ORA-00932`；`IN (SELECT COLUMN_VALUE FROM TABLE(:1))` 对 VARRAY/嵌套表都成立。
4. **Go 引擎与 Java 引擎都要实现**（sqlgen 两侧均维护；停维的是 datacenter 的 Go 端）。
5. 数组列值（`operations []string`）同样签名统一 + 各自实现，不降级为 string。
6. **方言特化 = 各自技术栈**：PG 用 `java.sql.Array`/`pq.Array`，MySQL/MSSQL 用 JSON（Jackson），
   Oracle 用 `oracle.sql.ARRAY`/go-ora 集合——每个方言用自己生态的原生能力，不追求跨方言一致实现。

## DSL 语法

### 数组成员

```sql
-- param: group_ids []int64
SELECT id, name FROM groups WHERE id = ANY(@group_ids)
```

`= ANY(@param)` 是唯一规范写法（表达数组成员）。

### IN 子句约束

- `IN (@x)`（括号内仅一个参数，不管是否数组）→ **生成时报错**，提示改用 `= ANY(@x)`（数组）或 `= @x`（标量）。
- `IN (1, 2, 3)` 字面量列表、`IN (@a, @b)` 多参数、`IN (SELECT ...)` 子查询 → 正常保留。
- `IN` 语法不用于处理数组参数。

### 数组列值

```sql
-- param: operations []string
INSERT INTO services_table (operations) VALUES (@operations)
```

无特殊语法，靠参数类型 `[]T` 识别。

## 签名统一

| DSL 类型 | Java | Go |
|---------|------|-----|
| `[]int64` | `long[]` | `[]int64` |
| `[]int32` | `int[]` | `[]int32` |
| `[]string` | `String[]` | `[]string` |
| `[]byte` | `byte[]`（已有默认支持，不生成 TypeHandler）| `[]byte` |

四方言的 mapper/runner 方法签名必须完全一致，仅 SQL 与绑定方式不同。

## 各方言实现

### 数组成员 `WHERE id = ANY(@ids)`

| 方言 | SQL | Java 绑定 | Go 绑定 |
|------|-----|----------|--------|
| PG | `id = ANY($1)` | `long[]` → `java.sql.Array`（`createArrayOf`）| `pq.Array(ids)` / pgx 原生 slice |
| MySQL | `JSON_CONTAINS(?, CAST(id AS JSON))` | `long[]` → JSON 字符串 | JSON 字符串 |
| Oracle | `id IN (SELECT COLUMN_VALUE FROM TABLE(:1))` | `long[]` → `oracle.sql.ARRAY`（`SYS.ODCINUMBERLIST`）| go-ora 集合 |
| MSSQL | `id IN (SELECT value FROM OPENJSON(@p1))` | `long[]` → JSON 数组字符串（Jackson，与 MySQL 同）| JSON 数组字符串 |

`[]string` 同构，仅类型名不同（PG `text[]`、Oracle `SYS.ODCIVARCHAR2LIST`、MySQL/MSSQL JSON 字符串）。

### 数组列值 `operations []string`

| 方言 | 列类型（DDL，datacenter 侧各自迁移）| Java/Go 绑定 |
|------|-----------------------------------|-------------|
| PG | `VARCHAR(32)[]` / `TEXT[]` | `java.sql.Array`（写）+ `getArray()`（读）|
| MySQL | `JSON` / `TEXT` | JSON 字符串（写）+ 解析（读）|
| Oracle | `VARRAY` / 嵌套表 或 `JSON` | `oracle.sql.ARRAY` 或 JSON |
| MSSQL | `NVARCHAR`（JSON 文本）| JSON 字符串 |

关键点：列值数组的 TypeHandler 需实现**读写双向**（`setParameter` + `getNullableResult`），
因为 SELECT 会把该列读回 model 字段。

## 空数组语义

**已定：B · 空数组 = 返回空结果**（数学上「空集成员」即无结果，不报错）。

各方言空数组行为：

| 方言 | 空数组 SQL | 结果 |
|------|-----------|------|
| PG | `= ANY('{}')` | 空集 ✓ |
| MySQL | `JSON_CONTAINS('[]', CAST(id AS JSON))` | 空集 ✓ |
| Oracle | `id IN (SELECT COLUMN_VALUE FROM TABLE(空集合))` | 见下注 ✓ |
| MSSQL | `id IN (SELECT value FROM OPENJSON('[]'))` | 空集 ✓ |

MSSQL 因此从 §14 的 `STRING_SPLIT` 改用 `OPENJSON`：`STRING_SPLIT('')` 会返回一个空串行
（`id IN ('')` 类型转换错配），而 `OPENJSON('[]')` 对空数组返回 0 行，天然正确。
MSSQL 与 MySQL 统一走 Jackson JSON 序列化，TypeHandler 可复用。

**Oracle 空集合绑定坑（实测）**：go-ora v2.9.0 绑定空/`nil` 集合会触发
`ORA-00600: [kokbgc2ip1]`（客户端序列化缺陷，非 Oracle 服务端问题——Java ojdbc 绑定空 ARRAY 正常返回 0 行）。
因此 **Go 引擎在框架方法里对空数组做短路**（`if len(arr) == 0 { return nil, nil }`），
不进入 DB 层；Java 侧 TypeHandler `createARRAY` 空数组经实测正常，无需短路。

注：若未来需要「空数组 = 报错」，在 TypeHandler `setNonNullParameter` 里检查 `length==0` 抛异常即可
（Go 在 runner 里 `len==0` 检查），四方言统一，属一行改动。

## 解析方案

vitess 无法解析 `= ANY(...)`（实测 syntax error）。采用**标记函数改写**：

1. `PreprocessSQL` 在 `replaceParams` 之前，新增 `replaceAnyMembership(sql)`：
   `= ANY(@arr)` → `= __sqlgen_any__(@arr)`（保留 `@arr`，不改变参数流）。
2. `replaceParams` 把 `@arr` → `?`，得到 `= __sqlgen_any__(?)`。
3. vitess 将 `= __sqlgen_any__(?)` 解析为 `ComparisonExpr{Left: col, Operator: "=", Right: FuncExpr{__sqlgen_any__, [?]}}`
   （已验证可解析）。
4. `mapComparison` 检测 `__sqlgen_any__`，产出新 AST 节点 `ExprAny{Left, Param}`。
5. 各方言引擎 render 该节点为各自的 SQL 模板（见上表）。

## 生成方案

### Java（MyBatis）

- 每个（方言 × 数组元素类型）生成一个 TypeHandler（如 `LongArrayTypeHandler`、`StringArrayTypeHandler`），
  放在对应方言 mapper 所在包（engineSubPackage 时进子包）。
- **写参数**：SQL 中用 `#{param, typeHandler=<FQN>}` 引用方言专属 TypeHandler。
- **读结果**：含数组列的 SELECT 生成 `@Results`，数组列逐列 `typeHandler=<FQN>`（方案 B，就地声明，
  不引入 Spring `@Component`/`typeHandlersPackage` 装配）。
- 签名统一：`@Param("ids") long[] ids`（四方言一致）。
- `[]byte` 不生成 TypeHandler（走 MyBatis 默认 `ByteArrayTypeHandler`）。

### Go

- 四方言 runner 签名统一 `[]int64` / `[]string`。
- 每方言在 runner 里做各自的绑定转换（PG `pq.Array`、MySQL/MSSQL JSON 序列化 `encoding/json`、Oracle go-ora 集合）。

## 开放问题

1. ~~JSON 序列化库~~ ✅ **已定：Jackson**（datacenter 已在 classpath；读回 JSON 列绕不开真解析器）。
2. ~~结果映射机制~~ ✅ **已定：方案 B**（写 `#{param, typeHandler=FQN}` + 读 `@Results` 逐列 typeHandler）。
3. ~~typeName 推导~~ ✅ **已定：硬编码映射**（`[]int64→bigint`/`[]int32→integer`/`[]string→text`/`[]float64→float8`；
   Oracle `SYS.ODCINUMBERLIST`/`SYS.ODCIVARCHAR2LIST`）。DSL 不显式声明。
4. ~~Go 侧 PG 驱动~~ ✅ **已定：lib/pq `pq.Array`**（datacenter Go 用 lib/pq；数组这一处打破「仅 database/sql」）。
5. ~~旧 `IN (@arr)` 兼容~~ ✅ **已定：不兼容**。`IN (@x)` 单参数报错，datacenter 6 处迁移到 `= ANY(@arr)`。
