# source：水位流的记录读取

流 `mdm.employee` 的记录读取（线协议 P9.30–P9.33）：员工、任职、账号关联三种记录，按身份读（`Read`）与按身份顺序分页（`Page`），每条是它当前的完整状态事件载荷（没有受限的个人资料）。这里从不删除记录，不认识的身份答“已清除”。

## 文件

| 文件 | 内容 |
| --- | --- |
| `source.go` | `kinds`、`query`、`Read`、`Page` |

## 依赖与被依赖

- 依赖：`core`。
- 被依赖：入口 `employee/definition.go`（`Stream.Records`）。

## 规则在哪

线协议第 09 章；装配项目协议 02。

## 测试

`itest/`（随 E3f）。
