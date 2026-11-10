# mdm/employee 的契约

组件 `mdm/employee` 对外发布的契约，随组件的版本发布：

| 文件 | 内容 |
| --- | --- |
| `openapi.yaml` | 用户面：员工、任职、账号关联的路由，每个路由的操作（`x-operation`） |
| `events/` | 流 `mdm.employee` 的三种事件的载荷（完整状态，受限的个人资料不进事件）；也是水位流记录读取（线协议 P9.30）答出的记录 |

系统面没有本组件自己的服务：水位补取与记录读取是 SDK 的 `be.source.v1`（`WatermarkService`、`RecordService`），补发、恢复核对、证据重放也是 SDK 的标准服务。以后有组件要直接调用员工组件（例如按岗位找持有人的审批分配）时，在这里加 `proto/`。
