# 按引擎分包设计 (engineSubPackage)

## 背景

Java 生成将所有 mapper 文件（共享接口、各引擎实现、Factory）放入同一个 `mapperPackage` 目录，引擎增多时文件数膨胀（4 引擎 × N 个 DSL → mapper 目录 5N+1 个文件）。

方案 A（已有）: `mapperPackage` 支持 `{stem}` 占位符按 DSL 文件分组。
方案 B（新增）: `engineSubPackage` 将引擎实现类放入 `${mapperPackage}.{engine}` 子包。

## 顺带重构：配置类型各归其位

当前 `GoPkgCfg` 和 `JavaPkgCfg` 一个在 `config.go`、一个在 `meta/types.go`，都不在各自的语言包里。本次一并移到各自 language 包下。

### Go: `languages/golang/config.go`（新建）

```go
package golang

// PkgCfg is a single Go package configuration.
type PkgCfg struct {
    Out   string   `yaml:"out"`
    Tags  []string `yaml:"tags"`
    Files []string `yaml:"files"`
}
```

`config.go` 中 `GoCfg` 改为：

```go
type GoCfg struct {
    Tags     []string        `yaml:"tags"`
    Packages []golang.PkgCfg `yaml:"packages"`
}
```

删除 `config.go` 中的 `GoPkgCfg` 定义。

### Java: `languages/java/config.go`（新建）

```go
package java

// PkgCfg is a single Java package configuration.
type PkgCfg struct {
    ModelPackage     string   `yaml:"modelPackage"`
    MapperPackage    string   `yaml:"mapperPackage"`
    Out              string   `yaml:"out"`
    Files            []string `yaml:"files"`
    EngineSubPackage bool     `yaml:"engineSubPackage"` // NEW
}
```

`config.go` 中 `JavaCfg` 改为：

```go
type JavaCfg struct {
    Packages []java.PkgCfg `yaml:"packages"`
}
```

删除 `config.go` 中的 alias，删除 `meta/types.go` 中的 `JavaPkgCfg`。

## 功能配置

```yaml
java:
  packages:
    - modelPackage: "com.dc.entity"
      mapperPackage: "com.dc.mapper"
      engineSubPackage: true    # 默认 false
      out: "src/gen/java"
      files: ["sqlgen/*.sql"]
```

## 产出结构

### engineSubPackage: false（默认，行为不变）

```
mapper/
├── ItemsMapper.java          # 共享接口
├── ItemsMapperPG.java        # PostgreSQL 实现
├── ItemsMapperMySQL.java     # MySQL 实现
├── ItemsMapperOracle.java    # Oracle 实现
├── ItemsMapperMSSQL.java     # MSSQL 实现
└── ItemsMapperFactory.java   # 工厂
```

### engineSubPackage: true

```
mapper/
├── ItemsMapper.java          # package com.dc.mapper（不变）
├── ItemsMapperFactory.java   # package com.dc.mapper，含子包 import
├── pg/
│   └── ItemsMapperPG.java    # package com.dc.mapper.pg
├── mysql/
│   └── ItemsMapperMySQL.java # package com.dc.mapper.mysql
├── oracle/
│   └── ItemsMapperOracle.java
└── mssql/
    └── ItemsMapperMSSQL.java
```

### 与 {stem} 组合

```yaml
mapperPackage: "com.dc.mapper.{stem}"
engineSubPackage: true
```

```
mapper/
├── approvals/
│   ├── ApprovalsMapper.java
│   ├── ApprovalsMapperFactory.java
│   ├── pg/ApprovalsMapperPG.java
│   └── mysql/ApprovalsMapperMySQL.java
├── users/
│   ...
```

## 实现要点

### 1. Generator — `languages/java/generator.go`

`Generate()` 签名改为：

```go
func Generate(pf *meta.ParsedFile, pkg PkgCfg, engs []Engine) error {
```

引擎循环内分流：

```go
enginePkg := pkg.MapperPackage
engineDir := mapperDir
if pkg.EngineSubPackage {
    enginePkg = pkg.MapperPackage + "." + eng.Name()  // com.dc.mapper.pg
    engineDir = filepath.Join(mapperDir, eng.Name())   // mapper/pg/
    os.MkdirAll(engineDir, 0755)
}
```

`writeJavaEngineMapper()` 接收独立的 `enginePkg`：

```go
func writeJavaEngineMapper(dir, enginePkg, mapperName string,
    specs []engines.RunnerSpec, modelType, modelPkg, mapperPkg string, eng Engine) error
```

### 2. Factory — `languages/java/factory.go`

```go
func GenFactory(mapperName string, engines []Engine, mapperPkg string, engineSubPkg bool) string
```

`engineSubPkg=true` 时生成 import：

```java
import com.dc.mapper.pg.ItemsMapperPG;
import com.dc.mapper.mysql.ItemsMapperMySQL;
```

### 3. 子包命名

使用 `eng.Name()`：`pg` / `mysql` / `oracle` / `mssql`。

## 变更文件清单

| 文件 | 变更 |
|------|------|
| `languages/golang/config.go` | **新建**，定义 `golang.PkgCfg`（从 config.go 搬出） |
| `languages/java/config.go` | **新建**，定义 `java.PkgCfg`（含 `EngineSubPackage`，从 meta 搬出） |
| `meta/types.go` | 删除 `JavaPkgCfg` |
| `config.go` | 删除 `GoPkgCfg` 定义和 `JavaPkgCfg` alias；`GoCfg`/`JavaCfg` 引到新位置 |
| `main.go` | pkg 循环变量类型自然变为 `golang.PkgCfg` / `java.PkgCfg`，无需改动 |
| `languages/java/generator.go` | `Generate()` 参数改为 `PkgCfg`；分流逻辑；writeJavaEngineMapper 签名改动 |
| `languages/java/factory.go` | `GenFactory()` 增加参数 |
| `golden_test.go` | `JavaPkgCfg{...}` → `java.PkgCfg{...}` |
