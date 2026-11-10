# replay：整套恢复后的重放

整套恢复第 6 步：按证据重新办理离职（并再结束未结束的任职）、任职结束、关联结束，不重做业务前置校验，只查对象还在（不在答“悬空”交复核）；事件与审计照常写，执行者是 `ops/recover`。

## 文件

| 文件 | 内容 |
| --- | --- |
| `replay.go` | `Replay`：按对象类型与变更种类分派给 `employees.ReplayTermination`、`appointments.ReplayEnd`、`links.ReplayEnd` |

## 依赖与被依赖

- 依赖：`appointments`、`core`、`employees`、`links`、`tightening`。
- 被依赖：入口 `employee/checks.go`（`Replays`）。

## 规则在哪

装配项目协议 15；线协议 P12.19–P12.21。

## 测试

`itest/evidence_integration_test.go`（`replay`）。
