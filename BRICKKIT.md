# mdm/employee

## 组件定位

员工与任职：按岗位或任职分配审批、授权的客户需要它；只按账号与角色管理的小客户可以不装。
- **员工**：一个人在一个法人的雇佣身份；工号在法人内唯一；预入职、在职、离职（生效日期，可以是将来的日期，到期由作业办理）。受限的个人资料（出生日期、证件号码）只给被授予可读字段的人，不进事件。
- **任职**：员工在一个法人、部门、岗位上的一段任职（`mdm/org` 的岗位）；不可复用的资格实例：调岗结束原任职、新建任职，不改原记录（安全 04）。同一人同一岗位的有效期不重叠。岗位或部门退役时，挂在上面的任职结束。
- **账号与员工的关联**：一个员工可以有几个账号，一个账号至多关联一个员工；建立与结束都是受保护操作，要授权组件保存的确认（安全 08）。
- **安全来源**：流 `mdm.employee` 是水位来源，授权组件补取它判断任职与关联；离职、任职结束、关联结束先写意图证据（协议 15）。

授予不在这里（任职维护权不等于授予权，D30）；法人、部门、岗位在 `mdm/org`。员工往来角色、导入、员工自助在契约里，本版答“能力不支持”。

## 部署前准备

- 数据库：PostgreSQL 16 及以上；schema `mdm_employee`，属主角色 `mdm_employee_owner`（只供迁移）与运行角色 `mdm_employee_rw`，由部署方在第一次启动前建好（装配项目的 `make db-init`）。
- 消息系统（`BUS_URL`）与实现实例（`IMPLEMENTATION_INSTANCE`）：流 `mdm.employee`（4 个分区）；订阅 `mdm.org`。
- 部署的服务证书（`SERVICE_TLS_*`）；身份与授权（`IAM_*`、`AUTHZ_GRPC_URL`、`DEPLOYMENT_ID`）；审计（`AUDIT_GRPC_URL`）。

## 依赖说明

- 必需：`mdm/org`（法人、部门、岗位的投影与事件）、数据库、消息系统、身份成员、授权成员（账号关联的确认核对、最后管理员的提示）。
- 可选：审计组件（兼任证据存储）。

## 配置指南

全部键见 `component.yaml` 的 `configSchema`（由 SDK 生成，测试核对）；本组件没有自己的键。

| 键 | 说明 |
| --- | --- |
| `DEPLOYMENT_ID`、`IAM_ISSUER`、`IAM_JWKS_URL`、`AUTHZ_GRPC_URL`、`SERVICE_TLS_CERT_FILE`、`SERVICE_TLS_KEY_FILE`、`SERVICE_TLS_CA_FILE`（必填） | 受保护的用户面与 mTLS 的系统面要用；授权成员的地址也用来核对账号关联的确认 |
| `PG_HOST`、`PG_DATABASE`、`PG_USER`、`PG_PASSWORD`、`PG_SCHEMA`（必填） | 自己的数据库，密码是密钥；迁移另用 `PG_OWNER_USER`、`PG_OWNER_PASSWORD` |
| `BUS_URL`（必填，密钥）、`IMPLEMENTATION_INSTANCE`（必填） | 消息系统；实现实例是安装时生成的随机身份，事件与水位补取的回答都带它（协议 02） |
| `AUDIT_GRPC_URL` | 审计组件的系统面，也是证据存储 |
| `RUN_PROFILE_URL`、`RUN_PROFILE_BUCKET`、`RUN_PROFILE_REGION`、`RUN_PROFILE_ACCESS_KEY`、`RUN_PROFILE_SECRET_KEY`（密钥）、`ALERT_WEBHOOK_URL` | 运行档案与告警渠道（协议 15）：证据存储不可达时收紧的意图写到这里 |

## 契约索引

- **用户面**：`contract/openapi.yaml`。操作 `mdm.employee.read`、`.manage`，`mdm.employee.appointment.read`、`.manage`，`mdm.employee.account_link.read`、`.manage`。
- **系统面**：SDK 的标准服务：水位补取 `be.source.v1.WatermarkService/Pull`（任何组件：授权组件，与用 SDK 流程内核按岗位分配审批人的业务组件；操作 `mdm.employee.system.pull`）、记录读取 `be.source.v1.RecordService`（任何组件，`mdm.employee.system.read`）、补发、恢复核对、证据重放。
- **事件**（流 `mdm.employee`，schema 在 `contract/events/`）：`mdm.employee.changed.v1`、`mdm.employee.appointment.changed.v1`、`mdm.employee.account_link.changed.v1`，都是完整状态。

## 外壳声明

不是外壳。可以被 Go 的基础外壳（`be/go-infra`）承载。
