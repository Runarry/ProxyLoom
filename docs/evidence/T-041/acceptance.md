# T-041 持久配置校验验收

独立 mTLS 监听器验证登记证书、Runner ID、构建摘要、架构、任务类型和可用槽位；Runner 只接收加密存储解出的本次冻结产物，没有数据库凭证、全局主密钥或公开原生配置上传接口。内部 validation.Submit 服务可供后续 T-033 使用。

归档的 `runner-sandbox-report.json` 与 `runner-control-report.json` 均为 passed，运行内前后源码摘要一致；相应 `*-output.txt` 为经过动态凭证检查的输出。

- Linux amd64 真实三内核各合法／非法配置通过：加密 PostgreSQL 提交 → 登记 mTLS 领取 → 固定内核校验 → fenced 结果与零字节结算。还验证取消、重复回传及旧租约拒绝。
- 实际隔离验证覆盖 TCP／UDP／IPv6／Unix 网络拒绝、私有文件拒绝、进程和资源限制、取消后清理、执行文件替换后仍使用冻结字节、sealed memfd 不可写。隔离无法建立时拒绝执行。
- 错误 CA、缺少／未登记证书、构建不符、错误类型和租约边界均有负例；真实内部响应通过 OpenAPI schema 校验。
- Windows 客户端／控制服务／配置测试、race、vet 与 Linux arm64 交叉构建通过。arm64 没有真实内核运行证据；Windows 不提供 Linux 校验隔离。

独立安全审查发现并修正了可执行路径替换窗口：复制并验证后封存 memfd，固定 helper FD，使用受限 execveat；Landlock 权限从宽泛动态库目录收窄为 ELF 解释器与明确 DT_NEEDED 文件。资源型运行时退出归类为基础设施错误；固定 MALLOC_ARENA_MAX 避免合成 sing-box 进程虚拟地址空间的间歇性超限。客户端同时使用请求发送时起算的本地时限，失联不会继续执行超过租约。

两个验证报告总摘要不同的唯一原因是其间 deploy/tools.lock.json 的直接依赖元数据更新；执行器、控制服务、协议和隔离源码未改动。最终关键源码 SHA-256：

| 文件 | SHA-256 |
| --- | --- |
| internal/runner/exec/sandbox_linux.go | ba76badfd31c4139a3500c797dcfc9a66e4b226d4662477002844b959895c987 |
| internal/runner/exec/run.go | dd42995368b40877f460769156743c96fdc9578ac7ea300def8040617593d276 |
| internal/runner/client.go | 15835ff35ddb963b7b806cdf34f47a9ea4fb6c47a8326461dbae0a2ed7161e3e |
| internal/runnercontrol/server.go | 233fb551917a88f24f98f4c67677c8e9838891e165bdf4013b9c2cc91475de00 |

三个自执行 helper 隔离用例在 race 构建中明确跳过，因为 race shadow VM 不符合 1 GiB 子进程预算；上述专用非 race Linux 验证实际运行这些用例，不将跳过计作通过。RLIMIT_NPROC 按 UID 计算，部署使用专用 Runner UID 并配合容器 pids 限制。

开发 mTLS 初始化脚本在独立临时目录验证：锁定内核摘要、CA 签名、SAN、证书／私钥匹配、登记证书摘要与三个构建、二次运行保留已有完整身份组通过；CA 私钥不落盘。可选 Compose 接入说明见 `docs/m1-input-contract.md`。生产证书生命周期和双架构发布仍属后续部署任务，G1 和能力 verified 状态不变。
