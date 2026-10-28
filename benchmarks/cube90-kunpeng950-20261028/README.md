# pause/resume 基准 · cube90（鲲鹏 950 / arm64）· 2026-10-28

同一份源码：GitCode boostkit/CubeSandbox v0.7.1 分支，checkout 2babb6f7（与 x86 对比机同日测试同 commit）。
统计口径：avg（req_avg = 单沙箱单次操作平均；wall_avg = 整批墙钟）。两窗口 W1/W2。

## 环境
- 机器：cube90 / 192.168.25.90，aarch64，Kunpeng 950 7592C，2 路 × 96 核 SMT2 = 384 线程，4 NUMA，2261GB
- 栈：cubetoolbox；cubemaster/cli、cubelet/cli、cubeops/opscli、templatecenter、cube-api、shim/runtime、nicl
  全部重建为 v0.7.1 + 2babb6f7（builder 镜像 cube-sandbox-builder:fix5，go 1.25.7 由 goproxy.cn 自动拉取；
  CubeTemplateCenter 的 Makefile 硬编码 GOARCH=amd64，arm64 上须 `make build GOARCH=arm64`）
- agent ext4：toolbox 与 component_versions/cube-agent/v0.7.1 两处同一份（md5 a2143c7c），version=v0.7.1
- 容器镜像：仍为现场 p1-v071-86f6d9b-20261022/*:local（未重建；pause/resume 热路径不涉及）
- 配置：host.memory.dirty_log=true（与 x86 一致；原现场为 false，本次改为 true）
- 模板：tpl-79af1c33012142d7a5a132fd（alias lzh-isnotfc-arm64-1028），镜像 127.0.0.1:15001/sandbox-code:isnotfc-20261022-arm64，
  cpu=2000m mem=2000Mi writable=20Gi xfs；本地 registry 容器 lzh-registry-15001（15001→5000）仅用于建模板
- 现场：空闲（0 沙箱），load 1.2→6.4
- 备份：/home/lzh/backup-cube90-pre-align-20261028（原 86f6d9b9 栈二进制 + MD5SUMS + agent 两处）

## 结果（ms，两窗口）

### pause
| C | req_avg | wall_avg | req_p95 |
|---|---|---|---|
| 1 | 30.1 / 30.9 | 30.4 / 31.2 | 33.4 / 32.9 |
| 5 | 50.3 / 51.1 | 55.4 / 56.1 | 61.9 / 56.5 |
| 10 | 73.2 / 77.1 | 87.7 / 94.4 | 87.3 / 92.5 |

### resume
| C | req_avg | wall_avg | req_p95 |
|---|---|---|---|
| 1 | 54.2 / 56.4 | 54.5 / 56.7 | 56.0 / 60.6 |
| 5 | 129.3 / 120.9 | 144.9 / 137.2 | 176.3 / 137.6 |
| 10 | 268.8 / 279.2 | 299.0 / 311.8 | 319.1 / 323.7 |

6 个日志零失败零重试。

## 与 x86 对比机（EPYC 9654，同代码同口径）对比（req_avg，取两窗口区间）

| C | pause 950 / 9654 | resume 950 / 9654 |
|---|---|---|
| 1 | 30.1–30.9 / 35.1–35.7 | 54.2–56.4 / 59.9–61.2 |
| 5 | 50.3–51.1 / 51.2–53.7 | 120.9–129.3 / 153.9–156.3 |
| 10 | 73.2–77.1 / 65.2–67.7 | 268.8–279.2 / 295.0–316.5 |

wall_avg（整批）：
| C | pause 950 / 9654 | resume 950 / 9654 |
|---|---|---|
| 1 | 30.4–31.2 / 35.5–36.1 | 54.5–56.7 / 60.2–61.5 |
| 5 | 55.4–56.1 / 59.9–62.3 | 137.2–144.9 / 166.5–170.5 |
| 10 | 87.7–94.4 / 86.6–91.5 | 299.0–311.8 / 335.2–368.0 |

观察：C1–C5 上 950 的 pause/resume 都更快（resume C5 快 ~20%）；C10 时 950 的 pause 略慢、resume 仍更快。
差异属两平台固有特性（同 commit、同 dirty_log、同 guest 内存规格），跨机只作趋势参考。

## 注意
- 本机栈已由 86f6d9b9 换成 2babb6f7（按照用户要求对齐 x86 测试的同一份代码）；备份见上。
- 旧模板（replica agent_version=v0.7.1-86f6d9b）仍可用：其 agent 缓存目录未被改动。
- 本地 registry lzh-registry-15001 为本次建模板而启，保留可复用。
