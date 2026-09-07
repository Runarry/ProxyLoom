# T-028 场景矩阵

入口：`node scripts/verify-live-chain.mjs`（进程内夹具 + 锁定 linux/amd64 内核）与 `node scripts/verify-isolation-compose.mjs`（容器来源限制）。能力状态仍为 `unverified`。未测 arm64、其它协议、客户端导入。

| 场景 | 预期 | xray | sing-box | mihomo | 观察 |
| --- | --- | --- | --- | --- | --- |
| 正向链 A→B | `/probe` 成功；目标来源为 B；A、B、目标均 `ok` | pass | pass | pass | 目标 `remote_addr` = B IP，不是 127.0.0.1 |
| 绕行反例（正确 B 凭证直连） | 失败；B `forbidden_source`；目标 `ok` 不增加 | pass | pass | pass | 不以 TLS/认证失败代替来源限制 |
| 原拓扑方向交换 B→A | 失败；B `forbidden_source`；不放宽 B 只接受 A | pass | pass | pass | 原正向白名单未改 |
| 独立反向拓扑 B→A | 成功；目标来源为 A | pass | pass | pass | 与原拓扑分离 |
| 节点复用 A→B、A→C、独立 B、D→B | 原节点不变；标签不串；出口分别为 B/C/B/B | pass | pass | pass | 独立 B 用开放来源夹具；共享出口不放宽原正向 B |
| 故障关闭：停 A、停 B、整链 | 新连接失败；无单跳、无其它出口、无直连 | pass | pass | pass | 基线用 `Seq()`；故障后目标不得再有 `accept`/`ok`；停 A 后 B `ok` 不得增加 |
| 生命周期：结束、取消、超时、失败 | 核心退出、监听关闭、任务目录删除 | pass | pass | pass | 故障探测使用新 SOCKS 连接 |
| 观察反例（开放 B） | 直连 B 成功且目标来源为 B | pass | pass | pass | 证明观察能发现绕行，而非只看到 HTTP 失败 |

Compose 烟测（不跑内核）：共享 CA 信任 A→B 成功、错误 CA 失败、正确密码直连 B 得 `forbidden_source`、停 A 后链失败。项目 `proxyloom-isolation-t028`，测完 `down`。
