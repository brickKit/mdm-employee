# appointments：任职

员工在 `mdm/org` 的一个岗位上的一段任职（安全 04）：不可复用的资格实例，法人、部门、岗位不原地改（调岗结束旧的、新建一条），只有备注原地改；一人同一岗位的有效期不重叠。任职的结束：手工、调岗、最后一天过去（到期作业）、离职（`Ended`，入口接成 `core.Base.Terminated`）、岗位或部门退役（`EndOnRetirement`，`mdm/org` 的投影排的作业）。

## 负责与不负责

- 负责：表 `appointment`（`unit_path` 是部门维度的事实，从投影抄来，部门移动时由投影改写）；路由：某员工的任职、任命、调岗、读取、改备注、结束、岗位的持有人；事件 `mdm.employee.appointment.changed.v1`（主题是员工）；任命前按投影核对岗位与部门启用、属于员工的法人；作业 `appointment-effective`（每分钟）与 `core.JobEndOnRetirement`。
- 不负责：员工（`employees`）；部门路径的改写（`orgview` 的钩子）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `appointments.go` | `Appoint`、`Transfer`、`GetAppointment`、`UpdateAppointment`、`EndAppointment`、`ListAppointments`、`Holders`；`Ended`、`EndOnRetirement`、`Effective`；`insert`（重叠检查）、`end`、`Published`、`todayOf`（事务外算法人的今天） |

## 依赖与被依赖

- 依赖：`core`（含 `Base.Org`）、`tightening`。
- 被依赖：入口（路由、作业、`Base.Terminated`）。

## 易错点

- 法人的“今天”要在事务外算：它可能要读投影、向 `mdm/org` 取（事务里嵌套事务、发网络调用都被拒，到期作业撞过一次 `NESTED_TX`）。

## 规则在哪

`docs/design.md` 的任职状态表与不变量；装配项目安全 04、07（部门维度）。

## 测试

`itest/appointments_integration_test.go`（`appointments`）；改坏检查在装配项目 `dev/rebuild/c1-data/e3d-mutants.txt`。
