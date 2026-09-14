# T-042 M3 真实执行分项证据

`m3-native/native-network.txt` 保存锁定 Xray、sing-box、Mihomo 经真实 PostgreSQL 和 mTLS Runner 完成单节点与完整两跳的执行。还覆盖同一 Runner 四个连通性任务首次并发成功、取消后 ≤5 秒回收、断第一跳不绕行、不重试节点失败，以及不可变结果历史。

该原始运行的 native 阶段通过，后续 browser 阶段因定位器失败，整体运行不计通过；浏览器已在独立后续运行修复并通过，记录位于 T-051。源清单用于界定当时实现，后续代码变更仍需对应验证。

`m3-sandbox/report.json` 为完整通过的沙箱回归，包含在线端口限制、UDP / Unix socket / 非路由 Netlink 拒绝、文件和进程约束，并回归三个锁定内核的离线校验。系统根证书缺失时不授权不存在的目录，HTTPS 仍正常进行证书验证。

后续完整 M3 回归已通过，见 [实际重启与完整运行](../T-045/durable-restarts/report.json)。[扩展原生负例](extended-native/report.json)覆盖三内核错误认证、目标状态异常、响应摘要不符、独立健康检查、字节及时间截断、第一跳断开无绕行。[生产部署](../T-054/summary.md)使用实际监听端口验证网络规则生效前后的可达性，并验证旧就绪记录不能启用新 Runner。

在线功能范围为 Linux/amd64 合成 SOCKS 场景；另有 [真实 Trojan 两跳回归](../T-058/live-chain/live-chain-report.json)，覆盖方向交换、节点复用和故障无直连。均不提升未验证协议组合、客户端或 arm64 的支持声明。
