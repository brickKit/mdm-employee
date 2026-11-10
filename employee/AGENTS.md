# employee/：入口与组装

组件对外的一个包：`main.go` 与外壳只引用它（`employee.Definition()`、`employee.New`）。这里只**组装**：把 `internal/` 下模块的函数登记成 SDK 要的路由、水位流与记录读取、证据的重放与恢复核对；业务一行也不写。

## 文件

| 文件 | 内容 |
| --- | --- |
| `definition.go` | `Version`、`GRPCPort`、迁移、`Keys`（没有自己的键）；`Definition()`：身份、流 `mdm.employee`（水位来源：补取者 `infra/authz`；记录读取交给 `internal/source`）、资源与操作（`core.Definitions()`）、补发、恢复、重放、证据 |
| `module.go` | `Module` 与 `New`；`Routes`（契约的全部路由，还没做的与本版不做的交给 `core.Unsupported`）；`handler` 适配 |
| `contract_test.go` | 单元测试：清单的 `configSchema` 与 `events`、OpenAPI 与路由一一对应、事件载荷合乎 schema、合并安全 |
| `migrations/` | 表 |

## 依赖与被依赖

- 依赖：`internal/core`、`internal/source`。
- 被依赖：`main.go`、外壳 `be/go-infra`、`itest/`。

## 测试

`go test ./employee/`。
