# orgview：mdm/org 的投影

员工组件要的 `mdm/org` 记录（协议 02）：法人（业务日期的时区）、部门（任职抄下的组织树路径，部门维度用）、岗位（任职挂在它上面）。从 `mdm.org` 的事件维护，每小时对齐、按来源方法修复，`mdm/org` 的纪元变了就重新对齐。不论哪条路径写入（事件、修复、对齐），`Changed` 钩子都反应：部门的新路径抄到它的未结束任职上；部门或岗位退役时排一个“结束上面的任职”的工作项（`core.JobEndOnRetirement`，由任职模块办理：结束任职是收紧，意图要在事务之外先写，不能在这里的写入事务里做）。

## 负责与不负责

- 负责：`View`（每个成员一份，钩子绑着这个成员，不放进程级的变量）；`OnEvent`（不认识的动作标为不完整再补取，P6.33、PJ36）；`LegalEntity`、`Unit`、`Position`（投影里没有就向 `mdm/org` 取来补上，实现 `core.OrgView`）；`Align`、`Repair`、`Realign` 与作业名；`source`（`mdm/org` 的来源方法，按聚合）。
- 不负责：结束任职（任职模块）；路由。

## 文件

| 文件 | 内容 |
| --- | --- |
| `orgview.go` | 上面全部 |

## 依赖与被依赖

- 依赖：`core`；`mdm/org` 的契约（`contract/v3.0.0-alpha.3` 起有岗位）。
- 被依赖：入口 `employee/module.go`（订阅、作业）；别的模块经 `core.Base.Org` 用它。

## 规则在哪

装配项目协议 02（投影与对齐）；线协议 P6.33、P9.12。

## 测试

`itest/orgview_integration_test.go`（`orgProjection`）；改坏检查在装配项目 `dev/rebuild/c1-data/e3b-mutants.txt`。
