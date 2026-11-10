# employees：员工的生命周期

员工（设计文档的状态表）：入职（入职日期晚于法人的今天是预入职，否则在职）、姓名与译名、受限的个人资料、离职（今天或之前立即办理，并经 `Base.Terminated` 结束员工之外随离职结束的东西；之后的日期先记下，由作业到期办理，到期前可撤回）、取消还没开始的入职。日期是法人的业务日期（时区来自 `mdm/org` 的投影）。

## 负责与不负责

- 负责：表 `employee`；路由 `/api/employee/employees` 的列表、入职、读取、修改、离职、撤回离职、取消入职；事件 `mdm.employee.changed.v1`（主题是员工）；个人资料只在单条读取里、按可读字段遮蔽（`authz.Mask`），不进列表、不进事件；工号在法人内唯一，同样的入职答原来的；作业 `employee-effective`（每分钟，到期的入职与离职）；`Today`（法人的业务日期）。
- 不负责：任职（离职时经 `Base.Terminated` 由任职模块结束）；最后管理员的提示（`Base.AdminWarning` 由关联模块答）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `employees.go` | `Hire`、`GetEmployee`、`UpdateEmployee`、`Terminate`、`WithdrawTermination`、`CancelHire`、`ListEmployees`、`Effective`、`ReplayTermination`、`ReplayScheduledTermination`（离职到了那天就不能撤回）；`Published`（事件与审计）、`write`、`change`、`read`、`answer`（遮蔽） |

## 依赖与被依赖

- 依赖：`core`（含 `Base.Org` 的投影）、`tightening`。
- 被依赖：入口登记路由与作业。

## 规则在哪

`docs/design.md` 的状态与规则；装配项目协议 07、14，安全 09a（受限字段）。

## 测试

`itest/employees_integration_test.go`（`employees`）；改坏检查在装配项目 `dev/rebuild/c1-data/e3c-mutants.txt`。
