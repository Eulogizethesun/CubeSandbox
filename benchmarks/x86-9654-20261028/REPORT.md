# CubeSandbox 全量源码对齐 + create/pause/resume 基准 · 2026-10-28 · x86 对比机

统计口径：**avg**（req_avg = 单操作平均；wall_avg = 整批平均）。
权威数据目录：results-20261028-clean/（干净环境，19:37–19:43，两窗口 + create C100×N100）。

## 一、部署对齐（本次改动）

源码：GitCode boostkit/CubeSandbox，分支 v0.7.1，HEAD = 2babb6f7（/home/lzh/CubeSandbox）
构建：builder 容器 cube-sandbox-builder:ubuntu2004（go1.25.7 / rustc1.89），
      环境变量 CUBE_VERSION=v0.7.1 CUBE_COMMIT=2babb6f7... CUBE_BUILD_TIME=<构建时刻>
重建并部署（全部 v0.7.1 + 2babb6f7）：
  cubemaster/cli、cubelet/cli、cubeops/opscli、templatecenter、cube-api（Rust musl）、
  containerd-shim-cube-rs、cube-runtime、nicl（仓库 contrib）
容器镜像（源码构建；本地 tag + .one-click.env 覆盖，pull 失败回退本地缓存）：
  cube-proxy:v0.7.1-amd64、cube-egress:v0.7.1-amd64、cube-lifecycle-manager:v0.7.1-amd64
  （proxy 构建副本用阿里云 Alpine 源；LCM 加 GOPROXY=goproxy.cn、distroless 经 gcr.m.daocloud.io）
备份：/home/lzh/backup-pre-realign-20261027（原混合栈二进制 + MD5SUMS）

### 排查中修复的三个部署级问题
1) 版本戳缺失：未注入 CUBE_VERSION 时 Makefile 回落到 git describe → v0.6.0，
   建箱要 cube-agent v0.6.0 → "component version not in warehouse"。注入 v0.7.1 解决。
2) 【关键】agent ext4 双份必须字节一致：
   构建期挂 /usr/local/services/cubetoolbox/cube-agent/cube-agent.ext4，
   建箱恢复挂 /data/cubelet/root/component_versions/cube-agent/v0.7.1/cube-agent.ext4。
   仅差 5 字节（编译时间串）时，恢复后 guest 内 cube-agent ~20ms 后死
   （kernel panic: Attempted to kill init! exitcode=0x4），所有新建模板 create 失败。
   修复：两处用同一份（缓存 → toolbox 复制）。换 agent 资产必须同步两处。
3) dirty_log 曾被重启重置（true→false），已恢复 true；排查用 settle gate 已撤销（x86 默认关）。

## 二、方法

模板：tpl-eafc8a2936c54876ad51c042（alias lzh-isnotfc-bench-1028）
  镜像 127.0.0.1:5000/sandbox-code:isnotfc-20261022-amd64
  cpu=2000m mem=2000Mi writable=20Gi backend=xfs
脚本：/home/lzh/bench/{create_bench.py,pause_resume_bench.py}（issue #13 口径）
阶梯：create C1/C5/C10（n=20/8/5）+ **create C100 × N100**；pause+resume C1/C5/C10（n=20/8/5）
settle 1s；两个窗口（C100 一个 100 轮）
环境：干净（无外部沙箱；前序 504 个同事沙箱已由其本人在 19:23–19:35 经外部 API 清除）

## 三、结果（avg，单位 ms；W1/W2）

### create（SDK 建箱）
| C | req_avg | wall_avg | req_p95 |
|---|---|---|---|
| 1 | 56.3 / 58.3 | 56.8 / 58.8 | 67.7 / 67.1 |
| 5 | 58.4 / 58.5 | 62.2 / 62.4 | 66.8 / 65.2 |
| 10 | 56.3 / 55.9 | 64.0 / 62.9 | 72.5 / 65.0 |
| **100（n=10000）** | **49.1** | **305.7**（每沙箱 3.06） | 71.0 |

### pause
| C | req_avg | wall_avg | req_p95 |
|---|---|---|---|
| 1 | 35.1 / 35.7 | 35.5 / 36.1 | 36.1 / 37.3 |
| 5 | 53.7 / 51.2 | 62.3 / 59.9 | 61.3 / 60.1 |
| 10 | 67.7 / 65.2 | 91.5 / 86.6 | 86.2 / 78.5 |

### resume
| C | req_avg | wall_avg | req_p95 |
|---|---|---|---|
| 1 | 59.9 / 61.2 | 60.2 / 61.5 | 63.1 / 63.7 |
| 5 | 153.9 / 156.3 | 166.5 / 170.5 | 187.2 / 178.5 |
| 10 | 295.0 / 316.5 | 335.2 / 368.0 | 336.7 / 426.1 |

全部 13 个日志零失败零重试（含 C100 的 10000 次建箱）。
日志：results-20261028-clean/{create-,pr-}{c1,c5,c10}[b].log、create-c100.log、env.log

## 四、注意事项

- 早前 results-20261028/ 下的窗口 1/2 是在 504 个外部沙箱 + load 24–34 下测得，
  pause 达 323ms（其中 get_info 确认 ~270ms/次，因 cube-api 列表要处理 504 条）；
  这些数字仅作噪声对照，不要用于结论。干净环境 pause C1 = 35ms、create C1 = 57ms。
- create C100 的 req_avg（49ms）低于 C1/C5/C10，是并发重叠效应；吞吐看 wall_avg=306ms/100 个。
- 换 agent 资产（cube-agent.ext4）时，toolbox 与 component_versions/<ver>/ 必须字节一致，
  否则新模板建箱会在 guest 内 panic（本日已实测踩坑并修复）。
- 旧模板（如 tpl-1c4946c1、tpl-c597320、tpl-b85e13f8）与当前 agent 一致，仍可使用。
