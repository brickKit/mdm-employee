# itest/：集成测试与测试索引

员工组件对着真实的 PostgreSQL 与消息系统、经它真实的接口的测试（L2）。构建标签 `integration`；一条业务流程一个文件。

## 怎样跑

- `make test-integration`（`go test -count=1 -tags integration ./itest/`）；环境变量 `BE_SDK_TEST_PG_ADMIN`、`BE_SDK_TEST_NATS_URL`（装配项目的 `itenv.sh`）。
- 测试每次删掉再建 schema `mdm_employee_it`；端口 28227、9227 与替身的 19301 要空着。
- **只有一个测试** `TestEmployeeEndToEnd`，按下表的顺序跑各步。

## 文件

| 文件 | 步 | 测的流程 | 被测的模块 |
| --- | --- | --- | --- |
| `orgview_integration_test.go` | `orgProjection` | `mdm/org` 的事件到达三种投影（等事件带来的版本，不等启动时对齐留下的）；不认识的动作（连完整载荷也不取）标为不完整、由修复补取；对齐取到没有事件的记录 | `orgview` |
| `employees_integration_test.go` | `employees` | 入职（今天在职、之后预入职）、同样的入职答原来的、工号唯一、法人未知被拒；个人资料只给能读它的授予（别人看到遮蔽）、不进列表与事件；按版本修改；取消未开始的入职；之后日期的离职先记下、可撤回；到期作业办理入职与离职；今天离职立即办理、再离职被拒；事件的主题 | `employees` |

共用的：

| 文件 | 内容 |
| --- | --- |
| `endtoend_integration_test.go` | `TestEmployeeEndToEnd`；`fixture`；`start`（世界、`mdm/org` 的替身与流、迁移并运行成员）；`publishOrg`、`change`、`projected`、`due`（让单例作业立即到期）、`at`、`client.do`、`serve`、`portFree` |
| `standins_integration_test.go` | `mdm/org` 的替身：按契约答三种聚合的来源方法 |

## 加测试

新流程加一个文件，在上表加一行；测试不为了通过而改。
