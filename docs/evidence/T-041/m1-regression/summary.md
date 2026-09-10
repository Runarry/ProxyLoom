# T-041 M1 编排窗口回归

2026-09-10，使用父任务专属 PostgreSQL 和锁定 linux/amd64 内核，在沙盒外运行：

```text
node scripts/verify-runner-control.mjs --network proxyloom-acceptance-500cca68-994b-4459-998e-788c7aebcdc7 --dsn-dir .cache/acceptance-db/500cca68-994b-4459-998e-788c7aebcdc7/secrets
```

结果 PASS，见 [报告](report.json)和[脱敏输出](output.txt)。真实路径覆盖加密持久提交、mTLS 注册／领取、三个锁定内核各一正一反检查、fencing、重放拒绝与零字节校验结果；无凭证和非信任 CA 证书均被拒绝。主实库用例 12.58 秒。

运行前后源码摘要一致：`aeb8cb7d0f5e65b5f22766444d8589e0328d33348f3e88dc0eb10bb3380f0234`。本次使用现有 T-041 请求／队列接口，不新增 M2 发布、订阅或冻结持久化 API。

之后仅针对三内核编排适配器做格式整理及专属映射限额／测试收尾；Runner、runnercontrol、runnerprotocol、validation、jobs 与其持久校验执行路径未改动。本记录不冒充针对所有新编排配置逐一通过持久队列的全矩阵结果；新配置最终字节的原生检查分别由 T-029 覆盖。
