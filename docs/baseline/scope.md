# P0 候选范围与未决登记

依据 SRS §2、§3.2 与 PLAN §2。P0 是单工作空间、可信管理员、自托管管理与测试平台；不改变远端协议、不承载日常代理网关、不静默降级、不在失败时直连。两跳只允许两个具体 Node，B 是最终出口，不修改原 Node。

以下为待细化并验证的候选，不能当作三内核支持矩阵或参数笛卡尔积。Xray、sing-box、Mihomo 的配置、网络、客户端验证状态全部为 `unverified`；每个目标最终支持判断须绑定实际锁定构建、参数、正负样例与证据。

| 候选 ID | 节点／传输／安全 | 需要细化的内容 | 状态 |
| --- | --- | --- | --- |
| P0-SS-TCP | Shadowsocks 常用 AEAD／原生 TCP／协议内置加密 | 具体 AEAD 方法、SIP002 方言及字段 | unverified |
| P0-VMESS-TCP | VMess AEAD／原生 TCP／none 或 TLS，分别测试 | AEAD 参数与 URI 方言 | unverified |
| P0-VMESS-WS | VMess AEAD／WebSocket／none 或 TLS，分别测试 | path、Host、SNI 与证书检查 | unverified |
| P0-VLESS-TCP | VLESS／原生 TCP／none 或 TLS，分别测试 | UUID、flow 与安全字段兼容限制 | unverified |
| P0-VLESS-WS | VLESS／WebSocket／none 或 TLS，分别测试 | path、Host 与 TLS 参数 | unverified |
| P0-VLESS-REALITY | VLESS／原生 TCP／REALITY 独立组合 | 公钥、short ID、server name、fingerprint、flow 等白名单与目标差异 | unverified |
| P0-TROJAN-TCP | Trojan／原生 TCP／TLS | 密码、SNI、ALPN、证书检查；可作为后续纵向原型示例 | unverified |
| P0-TROJAN-WS | Trojan／WebSocket／TLS | WebSocket 与 TLS 字段组合 | unverified |
| P0-SOCKS5-TCP | SOCKS5／原生 TCP／none | 无认证与用户名密码分别验证 | unverified |
| P0-HTTP-TCP | HTTP CONNECT／原生 TCP／none 或 TLS，分别测试 | 认证与 HTTPS 代理字段分别验证 | unverified |

六类协议及 TCP／WebSocket／TLS、独立 VLESS REALITY 的实施范围不因尚未验证而取消。表内具体方法、方言、flow、平台和客户端仍未冻结，由 BE-A／QA 在 T-009、T-022、T-023、T-029 细化；不能新增隐式默认值来绕过缺失输入，必需组合受阻须正式协调范围变更。

| 事项 | 当前结论／边界 | 责任角色与确认点 |
| --- | --- | --- |
| 角色与投入 | TL、BE-A、BE-B、FE、QA、OPS 为责任角色；实际人员、投入、独立审核人未指定 | TL；正式迭代承诺和基线评审前 |
| 外部网络测试许可 | 未提供；本轮不执行外部节点连接或测速 | TL／OPS；任何外部网络演示前取得明确许可 |
| 默认后续夹具 | 仅 T-053 自建隔离代理服务与 HTTP(S) 目标；临时凭证、可重建证书、test-only 私网例外 | BE-B／QA／OPS；T-053 实际实现与审核，当前尚未建立 |
| 生产网络 | Runner 无数据库凭证／全局主密钥；无任意 shell、路径或用户原生配置；网络预算与隔离需后续实现 | BE-B／OPS；T-040～045、054、057 |
| 内核／客户端 | 正式内核锁留 T-023；客户端导入与内核验证独立；尚无通过证据 | BE-A／QA；G0 候选登记、G3 全证据 |
| 许可／分发 | 开源及分发方式未指定，不把开发依赖锁当许可证审查 | TL／OPS；T-059、对外分发前 |

本轮开发允许 Windows PowerShell 与 Docker Linux 容器；生产门槛仍是 Linux amd64／arm64。Docker 可用性与实际烟测必须以 T-002 证据为准，不能由本基线推断已经通过。
