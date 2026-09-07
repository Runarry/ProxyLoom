# Runner 配置检查夹具

手写最小回环配置，供 T-040 在锁定 Debian 镜像中执行 `config_validate` 与启动回收。它们不是 T-024 编译产物，也不含真实节点凭证。

- `validate/*.valid.*`：仅 127.0.0.1 入口、直连出站、关闭控制面与自动下载。
- `validate/*.invalid.*`：故意损坏，锁定内核必须非 0 退出。

重建：`node scripts/pin-cores.mjs`（若缺归档）后 `node scripts/verify-core-exec.mjs`。
