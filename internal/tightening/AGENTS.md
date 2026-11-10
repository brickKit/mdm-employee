# tightening：收紧的证据

离职、任职结束、账号关联结束会收紧访问（授权组件撤销绑定它们的授予），所以先在事务外写意图证据，再在事务里提交，在锁下得出“是不是真的收紧了”的结论，没发生就取消（装配项目协议 15）。入职、任命、建立关联不收紧，不记；离职连带结束的任职归在离职那份证据里（重放离职时再结束它们）。登记在之后日期的离职与任职之后的最后一天现在不结束什么，但收窄了以后，登记时就记（整套恢复到登记之前不能忘掉它们；3.0.3 起）。

## 负责与不负责

- 负责：类别 `Category`（`employee_tightening`）与三种对象类型；`Tightening`；`Intent`、`Conclude`、`Settle`；`Termination`（今天或之前）、`ScheduledTermination`（之后日期，种类 `termination_scheduled`）、`AppointmentEnd`、`ScheduledEnd`（之后的最后一天，`appointment_end_scheduled`）、`LinkEnd`。
- 不负责：变化本身（`employees`、`appointments`、`links`）；重放（`replay`）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `tightening.go` | 上面全部 |

## 依赖与被依赖

- 依赖：`core`。
- 被依赖：`employees`、`appointments`、`links`、`replay`；入口（`Category`）。

## 规则在哪

装配项目协议 15；线协议第 12 章。

## 测试

`itest/evidence_integration_test.go`（`evidence`）。
