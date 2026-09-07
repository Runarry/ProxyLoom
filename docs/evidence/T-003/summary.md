# T-003 IR 与共享接口证据

状态：实现与本地测试通过，待最终评审。对应 AC-03／AC-08 的结构及静态语义子集，不代表 URI 导入、完整编译、原生内核或网络行为通过。

## 实现边界

- 根 Go module 的 `internal/ir`、`internal/adapter`；独立嵌入的 JSON Schema 2020-12 v1；详细契约见 `docs/ir-contract.md`。
- 六类节点的具体认证、TCP／WebSocket、none／TLS／REALITY、安全和 features 类型；无自由原生配置映射。扩展首版白名单为空。
- 两跳不同具体 Node，类型化引用和不可变冻结句柄；构造及取出均深拷贝，集合排序不会改写 hops／ALPN 顺序。
- 诊断使用稳定代码与 JSON Pointer，不带任意输入值；普通 fmt/slog 对凭证聚合类型脱敏，显式编译 JSON 则有意保留秘密。
- Compiler／RunnerAdapter 仅接口与受控命令规格，尚无实际编译器、进程执行或密钥服务。M0 快照只包含 node/chain 闭包及目标身份，完整预设／发布冻结由 T-022／T-032 扩展。

## 已执行验证

环境：Windows amd64、Go 1.26.0；Schema 验证依赖 jsonschema/v6 v6.0.2，由本地 embedded Schema 加载器执行，明确拒绝外部加载。起始提交为 `8acf714ad4420369b2c52502116fc961daa8c92d`，新代码尚未提交，最终 working tree 清单见 `../T-002/source-manifest.json`。

```text
go test -count=1 ./internal/ir ./internal/adapter ./schemas
go vet ./internal/ir ./internal/adapter ./schemas
go test ./...
go vet ./...
go mod verify
go mod tidy -diff
node scripts/check-locks.mjs
node scripts/check-plan.mjs
node scripts/check-boundaries.mjs
```

上述命令均实际通过；Schema 包本身没有独立测试文件，Schema 与 Go 一致性由 IR 的夹具测试执行，不将“无测试文件”视为自身测试通过。

测试覆盖：

- 24 个正负夹具及清单对照，区分 Schema 结构结论与 Go 引用语义结论；包括独立 Trojan A/B、A→B 和其他五类协议的代表输入。
- 未知／重复／大小写字段、未知版本、非法 JSON／UTF-8／UTF-16、整数等价表示与超出 float64 精度的修订值。
- 缺失认证、协议与认证类型不匹配、typed nil、混合联合分支、null 与缺省／false、Node 链字段和原生扩展拒绝。
- 三跳／重复节点／非 Node 跳、引用缺失／错误类型／错误修订／错误安全 epoch、跨 scope、禁用对象、重复目标及未被引用凭证拒绝。
- 冻结构造前后及取出副本的可变别名修改、集合稳定排序、跳顺序与 ALPN 顺序保留。
- 受控可执行文件标识／工作目录匹配、NUL 参数拒绝、命令参数与产物字节拷贝隔离，以及 Artifact 默认格式化／日志脱敏。

## 复核结果

独立只读复核后，修复了未知 JSON 字段名进入诊断的问题；未知字段退到安全父路径，非法资源 ID 不回显。父代理进一步发现直接 Go 构造的无效 UTF-8 会在 json.Marshal 时被替换，已在序列化前验证并增加回归。

复核提出的“转义引号后 UTF-16 surrogate 绕过”经回归验证为误报：原扫描器已正确跳过转义字符；保留实现并加入正负回归，未把未发生的缺陷写成已修复。

执行与集成复核：Codex 及 IR／foundation_review 子代理；没有人工验收签字。Linux race 最终结果见本轮集成补记。未执行任何真实内核命令、网络节点或客户端导入；全部能力仍为 unverified，T-023／024／040／053 及 G0 工作未包含在本次完成声明中。

最终集成补记：固定 Linux 工具链中的 `node scripts/check.mjs` 全部通过，包含 `go test -mod=readonly -race ./...`；IR、Adapter、配置、服务、存储、迁移与两个入口包均通过，Schema 由 IR 测试覆盖。具体检查镜像 ID 见 T-002 证据；T-003 已进入 `in_review`，不自动标 done。
