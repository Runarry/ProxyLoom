# ProxyLoom 自托管操作手册

适用范围：可信管理员管理的单工作空间；Linux Docker Engine 主机。交付脚本操作本机 Docker，不包含公网反向代理或生产环境的自动配置。arm64 镜像与运行验收分别记录；当前 arm64 保留未验，不能依据成功构建提升支持状态。

## 安装

主机需要 Docker Engine、Docker Compose v2 或更新兼容版本、Bash、util-linux 的 `nsenter`、iproute2、iptables/ip6tables；部署网络规则需要主机 root。API 和 Runner 分别以 UID 10001、10002 运行，不获得主机网络管理权限。PostgreSQL 官方入口只在初始化卷时处理权限，数据库常驻进程为 postgres 用户。

解压对应架构的交付包后，先校验 `SHA256SUMS`，再用 `docker load -i images-amd64.tar`（或对应 arm64 包）载入镜像。`images-<arch>.env` 同时记录唯一导入标签、清单摘要与配置摘要。安装脚本核对当前 Docker 存储后端返回的摘要后，只用对应的不可变本地 image ID 启动服务。不要手工换成浮动 tag。

```sh
sha256sum -c SHA256SUMS
docker load -i images-amd64.tar
export PROXYLOOM_STATE_DIR=/var/lib/proxyloom
sudo --preserve-env=PROXYLOOM_STATE_DIR bash deploy/proxyloom.sh init https://proxyloom.example.com
sudo --preserve-env=PROXYLOOM_STATE_DIR bash deploy/proxyloom.sh export-keys /secure/offline/proxyloom-keys
sudo --preserve-env=PROXYLOOM_STATE_DIR bash deploy/proxyloom.sh start
sudo --preserve-env=PROXYLOOM_STATE_DIR bash deploy/proxyloom.sh install-timers
```

`init` 只接受新的空状态目录，不覆盖已有密钥或数据库。状态目录主机权限为 0700，服务只挂载自己需要的单个秘密文件。单文件挂载为只读；不能把整个 secrets 目录挂进 Runner。独立密钥导出含敏感信息，应放在与数据库备份不同的加密介质或秘密管理设施中。

API 默认仅监听主机 `127.0.0.1:8080`。HTTPS 入口由管理员已有反向代理转发至该地址，公开 URL 必须与浏览器使用的源一致。仅本机演练可以在初始化时使用 `http://127.0.0.1:8080`。首次通过页面输入 `secrets/setup_token` 完成管理员设置；令牌不要放进 URL、日志或工单。

三个常驻服务为 API、Runner、PostgreSQL。迁移与运维命令为一次性容器。数据网络只连接 API 和数据库；控制网络只连接 API 和 Runner，使用 mTLS；Runner 另有出站网络，没有公开 SOCKS 端口。

## 联网隔离与资源

`start` 先启动数据库、迁移与 API，再启动 Runner 并写入其独立网络命名空间的 OUTPUT 规则。规则允许本容器回环和指定 API 的 TCP 9091，拒绝受保护地址、主机全局地址及非 TCP 外联。DNS 通过 Docker 的本地解析器；实际被测地址仍由冻结流程校验和固定。内核另受 Landlock、seccomp、文件和进程资源约束。

Runner 只有收到与自身 ID、主机 boot ID、当前网络命名空间和本次启动随机标识一致的就绪记录后才开始执行。随机标识避免 Linux 复用命名空间编号时接受旧记录。主机重启或容器重建使旧记录失效。30 秒 guard timer 会为新命名空间重新应用规则；新启动没有就绪记录时 Runner 保持未就绪。手工修改 Docker 网络后也应执行 `guard`。网络规则只写入本项目 Runner 的网络命名空间，不改主机防火墙。

API 默认上限 2 CPU/512 MiB，Runner 2 CPU/1 GiB，PostgreSQL 1 CPU/1 GiB；进程数均有上限。这些 CPU 值是各容器上限而非保留配额，4 核主机按实际负载共享；容量基线关闭公网测速，不能推导满载测速时仍具有相同管理接口时延。系统页面只能在部署上限内调整预算。连通性 4、吞吐 1 同时受全局和本机槽位约束。日志按每容器 2 个 5 MiB 文件滚动。关闭页面不取消服务器任务。

若 Runner 持续未就绪，先执行 `status`，检查 `network_guard_waiting`、Linux Landlock 支持、guard service 日志和 mTLS 证书有效期。新建身份后要同步 API 与 Runner 的证书、登记和 Runner ID。不要通过关闭证书验证或放开全部网络来解决故障。

## 每日备份与恢复

每日 UTC 00:00 的 systemd timer 执行加密备份，`Persistent=true` 补做关机期间错过的一次运行。首次安装后立即执行一次 `backup`。监控必须对最近成功备份超过 24 小时告警；计划任务的存在本身不能证明 RPO。备份文件自动生成唯一名称，脚本不会覆盖或自动删除旧文件。

```sh
sudo --preserve-env=PROXYLOOM_STATE_DIR bash deploy/proxyloom.sh backup
sudo --preserve-env=PROXYLOOM_STATE_DIR bash deploy/proxyloom.sh verify-backup /backups/example.age /secure/offline/proxyloom-keys/backup_identity
```

独立密钥清单：活动主密钥及其 ID、所有仍被引用的旧主密钥及映射、token pepper、内容 HMAC 密钥、age 恢复私钥。数据库口令可在新环境生成；Runner 登记与证书在恢复后重新生成。主密钥轮换后重新导出清单，旧备份仍需保存其对应 age 私钥及应用密钥。只复制数据库无法恢复节点秘密。

恢复必须使用新的空状态目录和不同的 Compose 项目名；原库继续隔离保留。新状态目录需要足够空间容纳加密文件副本与恢复后的数据库。恢复过程不落盘明文 dump。

```sh
export PROXYLOOM_STATE_DIR=/var/lib/proxyloom-recovered
export PROXYLOOM_PROJECT=proxyloom-recovered
sudo --preserve-env=PROXYLOOM_STATE_DIR,PROXYLOOM_PROJECT bash deploy/proxyloom.sh restore /backups/example.age /secure/offline/proxyloom-keys https://proxyloom.example.com
sudo --preserve-env=PROXYLOOM_STATE_DIR,PROXYLOOM_PROJECT bash deploy/proxyloom.sh start
```

恢复先完整校验文件和应用密钥，再在同一事务内导入、校验和升级 Schema、撤销旧会话/令牌/Runner 登记，并终结未结束任务。旧下载不重跑；未知已执行用量按原预留上限结算，未执行预留释放。缺密钥、截断、非空目标或最终校验失败均不提交恢复。完成后重新登录、确认节点可解密、生成新令牌并重新安装新项目 timer。RTO 必须以实际数据量的完整恢复演练计时。

## 升级与回退

先保留当前交付包，校验并载入新包，用**新包**脚本和原状态目录执行 `upgrade`。它先生成兼容旧 Schema 的加密备份，再停止 API/Runner，应用追加迁移并启动新镜像。失败时保留数据库与备份，不自动清库。

回退用候选旧包脚本执行 `rollback`；它要求该镜像与当前 Schema 完全兼容。M2 镜像不能读取 M3 新 Schema，不能只回退镜像 tag。需要跨不兼容 Schema 返回旧版本时，只能在隔离环境使用该版本支持的备份恢复程序演练并迁移服务，不能对原库执行逆向 SQL。

更新交付包路径后重新执行 `install-timers`，使 timer 指向新包。部署脚本不删除数据库卷；卸载数据需管理员另行明确操作。

## 指标与保留治理

`/metrics` 使用独立 `metrics_token` 的 Bearer 认证，不接受管理员 Cookie。配置该文件后才挂载指标路由。请求标签使用固定路由模板、方法和状态类；不含节点、目标、令牌或原始 URL。抓取端保持在本机或受保护管理网络，不将监控令牌放入查询串。

系统页面显示预算、队列和最近备份/清理时间，可暂停清理。默认结果保留 30 天、事件和脱敏日志 7 天、审计 180 天；活动发布、必要依赖修订和未结束任务按引用保护。清理按小批次执行，暂停不取消已有任务。磁盘满、数据库故障、备份超时和 Runner 失联分别检查对应服务状态与安全错误码。

完整阶段结论以 `docs/evidence/` 和 G3 验收表为准。本操作手册描述已实现命令，不代表双架构、20 分钟容量测试或 RPO/RTO 门槛已经全部通过。
