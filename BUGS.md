# Bug: Java Mapper 方法签名统一使用 System 类型

> ✅ **已修复（v1.2.1）**：`writeMethod` 改用 `spec.ModelType != "" && spec.Kind != RunnerReturningScalar` 选择返回/参数类型（不再用 `spec.IsScalar`），四个方言引擎统一。此处保留记录备查。

## 现状

DSL (`systems.sql`) 定义了 5 个 model，Entity 生成正确（5 个独立 Java record），但 Mapper 方法签名全部使用 `System` 类型:

| 操作 | 当前生成 (错误) | 应该生成 |
|------|----------------|---------|
| insertService | `long insertService(System item)` | `long insertService(Service item)` |
| findServiceByID | `System findServiceByID(long id)` | `Service findServiceByID(long id)` |
| findAppsBySystemID | `List<System> findAppsBySystemID(...)` | `List<App> findAppsBySystemID(...)` |
| insertAPIKey | `long insertAPIKey(System item)` | `long insertAPIKey(AppsSecret item)` |
| insertServicesTable | `long insertServicesTable(System item)` | `long insertServicesTable(ServicesTable item)` |

## 影响

Mapper 接口所有方法签名用同一个 `System` 类型，调用方必须做类型转换，失去类型安全。

## 根因推测

Java 引擎在生成 Mapper 时，对所有方法使用了包的第一个 model 作为参数/返回类型，而未按 `-- model:` 声明选择对应类型。

## 环境

- sqlgen v1.2.0
- Java 语言模块
- 复现 DSL: `systems.sql` (含 System/Service/App/AppsSecret/ServicesTable 5 个 model)
