# internal/：业务模块

员工与任职的业务，每个模块一个包。只给本仓库引用（Go 的 `internal`）：别的组件编译期就引用不了，跨组件只走契约（决定 0101）。

## 模块

| 包 | 一句话 | 依赖（本组件内） |
| --- | --- | --- |
| `appointments` | 任职 | `core` |
| `employees` | 员工的生命周期 | `core` |
| `links` | 账号关联 | `core` |
| `orgview` | `mdm/org` 的投影；实现 `core.OrgView` | `core` |
| `source` | 水位流的记录读取 | `core` |
| `core`（共用） | 名字与资源定义、校验、审计与事件、记录、列表的共用部分、`Base`、本版不做的路由 | — |

分层（每个包只引用比它低的层）：`appointments`、`employees`、`links`、`orgview`、`source` → `core`。员工模块要结束任职、问最后管理员时经 `core.Base` 上的钩子（`Terminated`、`AdminWarning`），由入口接上，不直接引用别的模块。别的模块经 `core.Base.Org`（接口 `core.OrgView`）用投影，不直接引用 `orgview`。

## 共同的写法

- 每个模块的函数先收 `m *core.Base`（运行时、证据写入器），入口包的 `Module` 持有它、登记时适配。
- 写入、审计（`core.Change`）与发件表（`core.Emit`，列出主题）同一个事务；跨组件的调用在事务之外。

## 测试

模块的业务规则由 `itest/` 的流程测试覆盖。
