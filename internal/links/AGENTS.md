# links：账号与员工的关联

一个账号同时至多关联一个员工，一个员工可以有几个账号；建立与结束都是受保护操作（安全 02、08）。确认在事务之前问授权组件（`AccountService.CheckLinkChange`，契约 `contract-infra-authz` alpha.6）：没带确认答 202、什么也不改；被拒答 `PERMISSION_DENIED`；此刻证明不了答 202 `processing`。员工版本在锁下再核对一次；关联记录带着确认，授权组件从本组件的水位流应用它时才把确认标为已用，所以本组件里一份确认只用一次（事务内检查加唯一索引）。离职不结束关联；离职与结束关联之前问授权组件哪些账号是授权管理员（`Administrators`），用于提示（D62），从不因此拒绝。

## 负责与不负责

- 负责：表 `account_link`；路由：某员工的关联、建立、结束、某账号的员工；事件 `mdm.employee.account_link.changed.v1`（主题是员工与账号）；`AdminWarning`（入口接成 `core.Base.AdminWarning`）。
- 不负责：确认本身（授权组件保存与消耗）；离职（`employees`）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `links.go` | `LinkAccount`、`EndLink`、`ListLinks`、`EmployeeOfAccount`、`AdminWarning`；`check`（问授权组件、写 202 或拒绝）、`used`、`unique`（并发的重复映射成同样的原因）、`administrators`、`Published` |

## 依赖与被依赖

- 依赖：`core`（含 `Base.Authz`：授权组件的 `AccountService`，入口用 `AUTHZ_GRPC_URL` 建）、`tightening`。
- 被依赖：入口（路由、`Base.AdminWarning`）。

## 易错点

- 事务前的预检（版本、离职、账号已关联）与锁下的复核、事务内的检查与唯一索引是两层，故意的：只去掉一层测不出来（改坏检查不单独改它们）。

## 规则在哪

`docs/design.md` 的“账号关联是受保护操作”“最后的授权管理员”；装配项目安全 02、08（第 21 行），D62。

## 测试

`itest/links_integration_test.go`（`links`，授权组件是 be-sdk-go `sectest` 的替身：`LinkCheck`、`LinkChecks`、`Admins`）；改坏检查在装配项目 `dev/rebuild/c1-data/e3e-mutants.txt`。
