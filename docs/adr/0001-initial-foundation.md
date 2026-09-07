# ADR-0001：首轮工程与接口基线

日期：2026-09-07。关联 T-001、T-002、T-003。状态：采用用户授权的实施选择，交付评审待定。补充 SDD ADR-01／03／05，不替换历史决策编号。

本轮只交付 WP-01 工程基座、类型化 IR 和共享接口；M0 其余 T-023～028、T-040、T-053 保留原依赖与范围，不在本轮宣称实现或 G0 通过。

1. 新代码统一使用 `proxyloom`／`PROXYLOOM_*`，品牌 ProxyLoom，保留历史文档编号。
2. 仓库根只有一个 Go module：`github.com/Runarry/ProxyLoom`。API 与 Runner 保留 `proxyloom-server/`、`proxyloom-runner/`，作为独立二进制入口；Vue 3＋TypeScript＋Vite 保留 `proxyloom-web/`，独立前端依赖和构建。共享 Go 类型放根 `internal/`，结构契约放 `schemas/`。这是设计示例 `cmd/api/`、`cmd/runner/`、`web/` 的实际目录映射，不建立重复入口或嵌套 module。
3. 依赖方向为 HTTP／任务入口 → 应用服务 → 领域模型／仓储接口；数据库实现依赖领域接口，领域不依赖数据库。Adapter 只依赖 IR 与共享编译契约，不依赖 HTTP、数据库或 Runner 执行器；编译仅消费冻结输入，输出字节确定，不读网络或当前时间。
4. API 不运行内核；Runner 与 API 共享契约不共享数据库权限或主密钥。Go module 共享只解决代码组织，不代表安全隔离已经实现。独立进程、最小授权和网络隔离按后续任务验证。
5. 开发入口支持 Windows PowerShell 与 Docker Linux 容器，Linux amd64／arm64 仍为生产验收目标。版本与工具来源集中于 `deploy/tools.lock.json`，不可使用 latest 或编造摘要；正式内核候选锁在 T-023 建立。

代价：根 module 统一依赖升级，后续需持续防止 API 存储实现流入 Runner 依赖图；共享 IR 变更须同步适配器与 Schema。现目录避免搬迁已有工程，生产构建仍需分别构建两个二进制与前端。适配器、数据库实现、网络执行及完整 API 契约不因本 ADR 获准就视为本轮已交付。

未决事项、责任角色与网络许可见 `docs/baseline/scope.md`。未指定实际审核人；独立审核与最终任务状态由后续真实评审记录确认。
