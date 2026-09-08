# T-053 隔离测试夹具与证据目录

2026-09-08 补充：唯一烟测项目、全状态库存、子网重叠及清理失败回归见 [M0 修复与补验](../T-028/remediation-2026-09-08.md)。下列历史夹具记录保留。

状态：实现与本地测试通过，待最终评审。这是 AC-09／AC-10 的前置，不是三内核方向验收或 G0。

## 夹具

- `internal/isolation`：测试 CA、Trojan TLS 代理、HTTP 目标、连接日志与脱敏。
- `proxyloom-fixtures`：Compose 用的 test-only 入口，不进入 API／Runner 镜像。
- `deploy/compose.isolation.yaml` 与 `deploy/Dockerfile.isolation`：独立内部网络，带 `proxyloom.test-only` 标签。
- `fixtures/isolation/README.md`：重建说明。密码为合成值 `EXAMPLE_ONLY_*`。

库默认绑定回环；进程内链测试中 A 使用 `127.0.1.1`，B 使用 `127.0.2.1`，以便源地址策略区分“经 A”和“直连 B”。Compose 地址与证书配置见下方评审修复记录。

## 已执行验证

起点提交：`93295278b3e2f66a13cd326d89071b2ddebbe7ec`。主机 Windows amd64。`go test -count=1 ./internal/isolation` 通过（约 1s）。

实测：

1. 经 A→B 访问 HTTP `/probe` 成功，响应含 `"ok":true`；A、B、目标日志均有 `ok`。
2. 使用正确密码直连 B 被 `forbidden_source` 拒绝；目标 `ok` 计数不增加。
3. 停止 A 后再拨链失败；目标 `ok` 计数不增加（无隐式直连）。
4. `RedactLog` 去掉明文密码；事件默认格式化为 `[REDACTED]`。
5. `compose.dev.yaml`、`Dockerfile.api`、`Dockerfile.runner` 不含 isolation include、`PROXYLOOM_TEST_ONLY` 或 TUN。
6. `scripts/check-boundaries.mjs`：API 与 Runner 不导入 `internal/isolation`。
7. 同一 `Dockerfile.check` Linux race 运行包含 `./internal/isolation`。

未执行：用 Xray／sing-box／Mihomo 客户端走该夹具（T-025～T-028）；未在 CI 中启动 isolation Compose。本窗口不把夹具私网例外设为生产默认。

## 2026-09-07 评审修复复核

基于同一起点提交 `93295278b3e2f66a13cd326d89071b2ddebbe7ec` 的本地未提交改动，不提升任务状态、能力状态或 G0 结论。

Trojan 握手和客户端到上游的转发现在共享同一个缓冲读者。新增真实 TLS 回归在修复前复现：头和 payload 合并写入、64 KiB payload 出现超时，分段写入收到不完整请求的 400；修复后三个用例均通过，目标收到完整 HTTP body 并原样响应。原有进程内 A→B 成功、直连 B 拒绝、停 A 失败用例继续通过。

Compose 的内部网络固定为 `172.30.253.0/24`，A/B/目标分别为 `.10`/`.20`/`.30`，B 白名单仅允许 A。A/B 监听 `8443`（非特权端口），避免 UID 10003 且 `cap_drop: ALL` 时无法绑定 443。进程入口拒绝 B 缺少白名单、任何显式空值、非法 IP 和空列表项；拒绝缺失或不匹配的证书/私钥。A 未配置白名单时允许测试客户端连接。

新增 `go run ./proxyloom-fixtures init-certs`，生成共享测试 CA 公证书和 A/B 叶证书、PKCS8 私钥到 `.cache/isolation/certs`，拒绝覆盖现有目录。CA 私钥不落盘；Compose 只读挂载各自证书和叶密钥，客户端加载 `ca.pem` 并校验 SNI。重建与轮换步骤见 `fixtures/isolation/README.md`。

本轮 Windows amd64 已执行并通过：

- `go test -mod=readonly -count=1 ./internal/isolation ./internal/capability`，以及两个包的 `-race -count=1` 检查。
- `go test -mod=readonly -count=1 ./proxyloom-fixtures`：生成后重新加载，真实 TLS 验证共享 CA 信任 A/B、错误 CA/SNI 失败；覆盖禁止覆盖、CA 私钥不导出及启动配置正反例。
- `go vet -mod=readonly ./...`，以及锁文件、内核锁、PLAN 与依赖边界检查。
- `docker compose -f deploy/compose.isolation.yaml config --quiet`。该检查只解析配置，不验证运行时源地址、挂载或权限。
- 合并后 `node scripts/check.mjs` 完整通过，包含格式、模块校验、依赖漂移检查、静态分析及全仓库 `go test -mod=readonly -race ./...`（Windows amd64）。

Go 使用工作区缓存避免默认缓存目录的权限限制。本轮 Docker 引擎连接报 permission denied，未执行 Linux race，也未执行新增的 Unix 权限/严格 umask 回归；上方历史 Linux 检查不覆盖本次修复。

按本轮确认范围，容器烟测后置，必须随后检查共享 CA、A→B 成功、正确密码直连 B 被拒绝、停 A 后失败。三内核客户端验收仍属后续任务。没有以 Compose 解析或本机 TLS 测试宣称容器链路已通过。
