# core：全组件共用的名字与写法（共用）

每个模块都引用、自己什么业务也不做的东西。改这里会影响所有模块。

## 文件

| 文件 | 内容 |
| --- | --- |
| `base.go` | `Base`：运行时、证据写入器、`Org`（这个成员的 `mdm/org` 投影）、授权组件的 `Authz`（`AccountService`）、钩子 `Terminated`、`AdminWarning` |
| `names.go` | 组件 ID、流与事件类型、`mdm/org` 的名字、资源与操作、维度（任职按法人与部门路径，员工与关联只按法人）、受限字段的读取键；`Definitions()` |
| `rules.go` | 错误原因、格式、`Unsupported`、`Check`、`Invalid`、`NotFound`、`Change`、`CheckNames`、`SearchOf`、`Build`、`Eq`、`Principal`、`ResourceOf` |
| `org.go` | `OrgLegalEntity`、`OrgUnit`、`OrgPosition`、接口 `OrgView`；`Retirement` 与 `JobEndOnRetirement` |
| `records.go` | `Employee`（含受限的 `Personal`）、`Appointment`、`Link` 与 `Scan…`、`Get…`；`Emit`（带主题）与 `EmployeeSubject`、`AccountSubject` |

## 依赖与被依赖

- 依赖：只有 SDK。
- 被依赖：全部模块与入口。

## 规则在哪

`docs/design.md`；装配项目协议 02、07、14、15，安全 02、04、07、08、09a。

## 测试

由 `itest/` 的流程测试间接覆盖。
