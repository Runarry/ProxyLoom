# ADR-0005：T-028 测试 CA 信任与真实链路入口

日期：2026-09-07。关联 T-028、T-040、T-053。状态：采用本窗口实施选择，交付评审待定。补充 ADR-0002／0004，不替换历史决策编号。

1. 真实链路验证冻结夹具地址后再 `Compile`，同一份原生字节交给锁定内核 `ValidateConfig` 与 `Run`。不手改 `dialerProxy`／`detour`／`dialer-proxy`。
2. Emit 仍无自定义 CA 字段；Runner `cleanEnv` 不继承 `SSL_CERT_FILE` 或宿主代理。测试 CA 只安装进**本轮临时 Linux 容器**的系统信任（`update-ca-certificates`），内核保持 `verify_certificate=true`／`allowInsecure:false`／`skip-cert-verify:false`。
3. 核心入口保持回环。进程内矩阵用 `127.0.0.0/8` 别名区分 A／B／C／D／目标来源；Compose 烟测用 `172.30.253.0/24` 验证容器来源限制。
4. 观察基线含代理事件、目标 `remote_addr` 与 `accept`。绕行反例必须看到 B 的 `forbidden_source`，不能只用 HTTP 失败或目标计数不变。另用开放 B 的受控反例证明观察能发现成功绕行。

代价：Windows 宿主仍不能本地跑 linux 内核；arm64、其它协议和客户端导入仍未验证。能力状态保持 `unverified`。G0 需人工评审，本 ADR 不宣布关卡通过。
