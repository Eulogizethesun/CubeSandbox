# Host cgroup early-join policy

This tree is the formal implementation of the early-join policy. It keeps the
resume instrumentation from `CubeSandbox-pauseprof-resume` and makes Cubelet
skip the early join automatically when the sandbox cgroup has no effective
cpuset. There is no new config switch.

## Which system call is slow

The shim's early join calls `std::fs::write` on each existing
`/sys/fs/cgroup/<controller>/<pool-path>/cgroup.procs`. That is a pair of
syscalls:

1. `openat(2)` on `cgroup.procs` (about 18-20 us in the measurement);
2. **`write(2)` on the open `cgroup.procs` file descriptor** (16-75ms on
   950, 7-25ms on 9654 for the first migration of a fresh shim process).

The expensive interface is the **`write(2)` syscall**, not `openat(2)`, not
`stat(2)`: the `is_dir()` checks are 2-5us, and the same shim's second
controller write is 20-60us. The kernel handler is
`cgroup_procs_write()` (kernel/cgroup/cgroup.c), which triggers the cgroup
migration; the exact inner kernel function that dominates the 16-75ms is not
yet named.

Measurement used to separate the two calls: a fresh child process opens a
fresh cgroup's `cgroup.procs` and times open and write separately. One run:
`open=20.5us, write=39637.8us`; following runs in the same measurement
process were ~18us/29us. The shim's per-controller log times the whole
`std::fs::write`, so the early-join stage in the trace is dominated by the
`write(2)`.

## Policy

No config field. On every create/resume, Cubelet checks whether the assigned
sandbox cgroup has an effective cpuset:

- cgroup v1/legacy: `/sys/fs/cgroup/cpuset/<pool-path>` exists;
- cgroup v2 unified: `<pool-path>/cpuset.cpus` or `cpuset.mems` exists;
- hybrid: either of the above;
- stat errors other than ENOENT keep the early join (conservative).

If an effective cpuset exists, inject `cube.sandbox_cgroup_path` as before
(the shim joins before the VMM starts, preserving NUMA first-touch
semantics). If it does not, omit the annotation: the shim's
`join_host_cgroup()` is a no-op, and Cubelet's existing late `setCgroup`
places the shim/VMM after the container start/probe/envd stages.

The policy lives in `Cubelet/services/cubebox/cgroup_early_join.go`; the
annotation is built by `sandboxCgroupPathAnnotation()` in
`cube_container_create.go`. The shim itself is unchanged.

## Why direct (no config)

Both measured platforms have a net benefit:

| arm | 950 | 9654 |
|---|---:|---:|
| early join | 100.8, 110.5 (mean 105.7) | 102.5, 101.8 (mean 102.2) |
| skip when no cpuset | 67.4, 70.6 (mean 69.0) | 91.6, 93.1 (mean 92.4) |

- 950: −36.7ms (−35%), matching the 36.0ms first-write stage.
- 9654: −9.8ms; the 19.2ms cgroup saving is partly offset by a 10-14ms
  `new_from_snapshot` penalty, but the net is still positive.
- Skip-cpu-only is worse on 950 because the memory first write pays the same
  cost; skipping the whole early join when there is no cpuset is the useful
  case.

A config switch was considered and rejected: the check is deterministic from
the cgroup layout, and both platforms are net-positive.

## Safety

- If the sandbox cgroup has an effective cpuset, the early join is kept; the
  late move cannot reliably migrate already-faulted, pinned guest pages.
- When the early join is skipped, there is a ~50-100ms window in which the
  shim/VMM is outside the sandbox cpu/memory cgroup. Quota/accounting/OOM
  attribution for that window is deferred to `setCgroup`. Verify the final
  `/proc/<shim>/cgroup` and decide whether the window is acceptable.
- The late `setCgroup` must remain enabled; it is the correctness fallback.
- `disable_host_cgroup` is unrelated: it only controls host limit setting,
  not membership or this policy.

## Build and test

Cubelet changes only; the shim remains the instrumentation tree.

```bash
cd Cubelet
go test ./services/cubebox -run 'HostCgroup|EarlyJoin|SandboxCgroupPath' -count=1
go build ./...
```

Full data/analysis: `0.7版本的性能优化/38-resume阶段分解与cgroup首写-20261030.md`.
