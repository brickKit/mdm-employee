# mdm/employee

员工与任职组件的代码。用法与边界在 `BRICKKIT.md`，为什么这样设计（术语、数据、状态与规则、未决问题）在 [docs/design.md](docs/design.md)；跨组件的规则在装配项目 be-assembly-standard 的协议 02（水位来源）、07（生命周期）、14（业务日期）、15（意图证据），安全 02、04、08。

## 代码地图

代码按业务模块分层。**先读这一节，再进目录**：每个代码目录都有自己的 `AGENTS.md`（同目录的 `CLAUDE.md` 让 Claude Code 读这个目录的文件时自动带上它），写这一层负责什么、不负责什么、每个文件、依赖谁、测在哪。

| 目录 | 是什么 |
| --- | --- |
| [`employee/`](employee/AGENTS.md) | 入口：组件定义与组装（路由、水位流与记录读取、证据的重放、恢复核对都在这里登记，转给业务模块）；不写业务。`main.go` 与外壳只引用它 |
| [`internal/`](internal/AGENTS.md) | 业务：下面的模块各一个包；模块之间的依赖表在它的 `AGENTS.md` |
| [`itest/`](itest/AGENTS.md) | 集成测试，按业务流程分文件；它的 `AGENTS.md` 是测试索引 |
| `employee/migrations/` | 表：员工、任职、账号关联 |
| `contract/` | 本组件发布的契约：`contract/openapi.yaml`、`contract/events/` |
| `docs/design.md` | 设计 |

业务模块（`internal/` 下）：

| 模块 | 负责 | 拥有的表 |
| --- | --- | --- |
| [`appointments`](internal/appointments/AGENTS.md) | 任职：任命、调岗、结束、持有人；同岗不重叠；离职、退役、到期时结束 | `appointment` |
| [`employees`](internal/employees/AGENTS.md) | 员工：入职、修改、离职（今天或将来）、撤回、取消入职、列表；个人资料按可读字段遮蔽；到期作业 | `employee` |
| [`orgview`](internal/orgview/AGENTS.md) | `mdm/org` 的投影（法人、部门、岗位）：事件、对齐、修复；退役时排结束任职 | `org_realign`（投影本身在 SDK 的表里） |
| [`source`](internal/source/AGENTS.md) | 水位流的记录读取（三种记录，按身份与按身份顺序） | — |

共用的：[`core`](internal/core/AGENTS.md)（名字与资源定义、校验、审计与事件的写法、记录的类型与读取、列表的共用部分、本版不做的路由）。

（账号关联、收紧的证据与重放随 C1 的 E3e–E3f 加入，见装配项目 `dev/rebuild/c1-employee-plan.md`。）

## 构建与测试

- `go test ./...`（`make test`）：清单的 `configSchema` 与 `events`、OpenAPI 与路由、事件载荷、合并安全。
- `make test-integration`（`go test -tags integration ./itest/`）：真实的 PostgreSQL 与消息系统。
- `brickkit lint`

## 设计取舍

- **任职是资格实例，不原地改**：法人、部门、岗位变了就结束旧任职、新建任职；授权组件按任职实例绑定授予，结束即撤销，不会因为原地改而让旧授予跟着到新岗位（安全 04）。
- **确认在事务外核对、在授权组件应用时消耗**：跨组件调用不进事务；本组件里确认唯一，防止授权组件应用之前被用第二次。
- **契约没有自己的 proto**：系统面全是 SDK 的标准服务（水位补取、记录读取、补发、恢复、重放）；有组件要直接调用时再加。

## 易错点

- 法人、部门、岗位不原地改任职：结束原任职、新建任职（安全 04）。
- 账号关联不按姓名、邮箱、工号自动匹配；建立与结束都要确认，确认只用一次（安全 02、08）。
- 跨组件的调用（授权组件的确认核对）不进事务：先在事务外核对，再在事务里核对版本（P4.20）。

## 改代码前自查

- 改了版本号吗？每次改动都升版本。

<!-- brickkit:managed:begin lang=zh -->
<!-- 由 brickkit 维护（init、add、remove、upgrade、skills update）：这对标记之间的改动会被覆盖 -->

## BrickKit

这是一个 BrickKit 组件：平台只读 `component.yaml`。它依赖的规则：

- `configSchema` 的键就是代码读的环境变量名。不能用保留名：`COMPONENT_ID`、`COMPONENT_VERSION`、`PORT`、`BRICKKIT_SERVED_MEMBERS`、`BRICKKIT_SERVED_MEMBERS_CONFIG`，以及任何 `*_ENDPOINT`。
- 依赖写精确版本。依赖的地址以 `<ID>_ENDPOINT` 注入；缺席的可选依赖根本没有这个变量，读的时候要带兜底。
- `/healthz` 只查本进程，不查依赖。迁移命令用同一个镜像跑，遇到不认识的参数必须直接失败；`local.migrationCommand` 从源码跑同一条迁移，给 `mode: local` 用。
- `BRICKKIT.md` 会随版本进入每个使用它的项目，在那里是脱离仓库单独读的：跟代码一起改，不放相对链接。
- 发版：改 `metadata.version`，提交、推送，`brickkit release`。`brickkit lint` 会检查清单和这些文档——在项目里、在这个目录下跑，只查这个组件（`--all` 查整个项目）。
- 完整规则在 `brickkit-component` 技能里（装了技能的项目或仓库根目录下的 `.claude/skills/brickkit-component/SKILL.md`；`brickkit skills update` 会装上）；参数问 `brickkit <命令> --help`；BrickKit 自己的文档用 `brickkit docs` 看。
<!-- brickkit:managed:end -->
