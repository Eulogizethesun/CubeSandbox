# io-bench — 磁盘 / 文件系统 IO 基准（fio + iobench）

用来做**跨机或回归的磁盘与文件系统 IO 能力标定**：顺序/随机读写的吞吐与延迟、fsync、
reflink 克隆、fallocate、page cache 与 O_DIRECT 的差异。`950 vs 9654` 能力图谱里的 IO 数据
就是这套跑出来的，包括 **fio 4K 随机读 QD32**（950 领先 1.54×）这个用例。

## 文件清单

| 文件 | 作用 |
|---|---|
| `run_io.sh` | 一键驱动：`iobench` 各模式 + `fio` 的 5 个标准用例；结果写 `RESULT,<test>,<variant>,<size>,<threads>,<value>,<unit>` 行 |
| `iobench.c` | 文件/IO 微基准：`clone` / `clone_frag` / `clone_chain`（reflink 克隆）、`fsync`、`fallocate`、`pcache`（page-cache 随机读）、`odirect`（O_DIRECT 随机读 4K/64K/1M） |
| `fsctl.sh` | FICLONE 控制实验：在**相同 geometry** 的 loopback XFS 上复测，剥离部署 FS 布局（agcount 等）的影响 |
| `aggregate.py` | 把两台机（或两轮）的 `RESULT,*` 行聚成对比表；自带 lower/higher-better 判定，`advantage` 列 = 第一组/第二组 |

## 前置条件

- `fio`、`gcc`、`python3`
- `fsctl.sh` 需要 root（`mount -o loop`）
- 安静盘窗口：不要在别的测试/业务读写时跑；跑前记录 `loadavg`，别用 `drop_caches`
- 输出目录所在盘要有 ≥ 8G 空闲（fio `size=4G` + 克隆文件）

## 快速开始

```bash
# 一把跑完（约 1 分钟）：iobench 各模式 + 5 个 fio 用例
bash io-bench/run_io.sh <workdir> <out.csv> <log.txt>

# 跨机对比（两台机各跑一遍后）
python3 io-bench/aggregate.py --m950 950-r1.csv 950-r2.csv --m9654 9654-r1.csv 9654-r2.csv --out <dir>

# 相同 geometry 的 FICLONE 控制实验（8G loopback XFS：agcount=16、reflink=1、rmapbt=1）
bash io-bench/fsctl.sh <workdir>
```

## fio 标准用例（均 `--ioengine=libaio --direct=1 --size=4G`，8 秒 time-based）

| 用例名 | 参数 | 输出 |
|---|---|---|
| `seqwrite_1M_qd32` | `--rw=write --bs=1M --iodepth=32` | iops / bw / lat |
| `seqread_1M_qd32` | `--rw=read --bs=1M --iodepth=32` | iops / bw / lat |
| `randread_4K_qd1` | `--rw=randread --bs=4K --iodepth=1` | iops / bw / lat |
| **`randread_4K_qd32`** | `--rw=randread --bs=4K --iodepth=32` | iops / bw / lat |
| `randwrite_4K_qd1_sync` | `--rw=randwrite --bs=4K --iodepth=1 --fsync=1` | iops / bw / lat |

### iobench 模式

| 模式 | 测什么 |
|---|---|
| `clone` / `clone_frag` / `clone_chain` | reflink 克隆：连续文件 / 碎片化文件 / 链式克隆的耗时 |
| `fsync` | 1MB 文件 fsync 绝对延迟 |
| `fallocate` | 预分配耗时 |
| `pcache` | page-cache 随机读（不碰盘，测内存路径） |
| `odirect` | O_DIRECT 随机读 4K / 64K / 1M（绕页缓存，测盘） |

## 指标怎么看

1. **先分 QD 维度**：QD1 看**单 IO 延迟**（ns/µs），QD32 看**多流并行吞吐**（IOPS / MiB/s）。
   两者形态可以相反——950 在 4K 随机读上 QD1 是 0.92×（慢）、QD32 是 1.54×（快）。
2. **`--direct=1` 绕过 page cache**，读到的是盘；`pcache` 模式对照读的是内存路径。
   两者差距大 = 页缓存命中率/内存带宽影响大。
3. **`fsync` / `clone` 属文件系统+内核路径**（日志、extent 记录、btree），和 `odirect` 的盘路径分开看。
4. **跨机比较的硬约束**：盘型不同（如 950 Huawei 3.0T vs 9654 Intel 894G）时，绝对数只能当
   “当前部署形态”参考，不能归因到 CPU/内核；要归因必须在同一盘、或 `fsctl.sh` 相同 geometry
   上做控制实验。`aggregate.py` 的 `advantage` 列 >1 表示第一组更好（lower-is-better 项已自动翻转）。
5. **稳定性**：fio 用 8 秒 time-based，短窗内别叠加其他负载；同一窗口内做 A/B，跨窗口只比比值。

## 参考结果（950 鲲鹏 vs 9654，2026-10-27；两台盘型不同，仅作部署形态参考）

950 = Huawei 3.0T NVMe，9654 = Intel 894G NVMe。

| 用例 | 950 | 9654 | 950/9654 |
|---|---|---|---|
| 4K 随机读 QD1 | 14,928 IOPS / 66.8µs | 16,182 / 61.4µs | 0.92 |
| **4K 随机读 QD32** | **381,458 IOPS / 1,490 MiB/s / 83.6µs** | 247,534 / 967 / 129.0µs | **1.54** |
| 4K 随机写 QD1+fsync | 72,741 IOPS | 56,328 | 1.29 |
| 1M 顺序读 QD32 | 7,148 MiB/s | 5,753 | 1.24 |
| 1M 顺序写 QD32 | 4,488 MiB/s | 1,764 | 2.54 |
| O_DIRECT 随机 4K / 64K / 1M | 15,148 / 6,566 / 1,701 IOPS | 16,210 / 5,384 / 1,732 | 0.93 / 1.22 / 0.98 |
| page-cache 随机 4K | 930 ns | 858 ns | 0.92 |
| fsync 1MB | 240 µs | 306 µs | 1.28 |
| FICLONE 碎片 1G（相同 loopback geometry 控制实验） | 385 ms | 671 ms | 1.74 |

## 坑

- `size=4G` 会在工作目录建大文件，跑前 `rm -f` 旧文件，跑后清理；文件系统要支持 `reflink`（`cp --reflink=always` 失败即不支持）。
- `fsctl.sh` 需要 root 且不能在已有挂载点上重复挂；脚本自带 trap 清理。
- fio 的 `bw` 是字节/秒，脚本里已换 MiB/s；`lat_ns.mean` 是 ns。
- `aggregate.py` 只认 `RESULT,` 开头的 7 字段行；自己加用例时保持格式。
