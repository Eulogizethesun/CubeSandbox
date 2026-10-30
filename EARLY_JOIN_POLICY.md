# Host cgroup early-join policy

This tree is the formal implementation of the early-join policy. It keeps the
resume instrumentation from `CubeSandbox-pauseprof-resume` and adds a
Cubelet-side decision: only hand the sandbox cgroup path to the shim when the
early join has a semantic that cannot be applied later.

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
yet named (it is not the cpuset lookup itself).

Measurement used to separate the two calls: a fresh child process opens a
fresh cgroup's `cgroup.procs` and times open and write separately. One run:
`open=20.5us, write=39637.8us`; following runs in the same measurement
process were ~18us/29us. The shim's per-controller log times the whole
`std::fs::write`, so the early-join stage in the trace is dominated by the
`write(2)`.

## Policy

New Cubelet `common` config:

```yaml
host_cgroup_early_join: always   # always | auto | never
```

- `always` (default, including an empty value): inject
  `cube.sandbox_cgroup_path` exactly as before. No behavior change.
- `auto`: inject the annotation only when the sandbox cgroup has an
  **effective cpuset**:
  - cgroup v1/legacy: `/sys/fs/cgroup/cpuset/<pool-path>` exists (separate
    cpuset hierarchy);
  - cgroup v2 unified: `<pool-path>/cpuset.cpus` or `cpuset.mems` exists;
  - hybrid: either of the above;
  - stat errors other than ENOENT keep the early join (conservative).
  If there is no effective cpuset, the annotation is omitted; the shim's
  `join_host_cgroup()` becomes a no-op and Cubelet's late `setCgroup`
  remains the only mover.
- `never`: never inject the annotation; late `setCgroup` is the only mover.

Unknown values fall back to `always` with a warning. The policy is evaluated
in `Cubelet/services/cubebox/cgroup_early_join.go`; the annotation is built by
`sandboxCgroupPathAnnotation()` in `cube_container_create.go`.

## Why not simply “no cpuset directory -> skip”

That is the right core idea, but it is not sufficient as a formal check:

- cgroup v1 has a separate cpuset hierarchy; cgroup v2 uses the unified
  hierarchy. Checking only `/sys/fs/cgroup/cpuset/...` would wrongly skip on
  cgroup v2.
- The early join writes cpu and memory too. Skipping it removes their
  membership/accounting during restore, even though Cubelet sets host limits
  after `createContainers` and late `setCgroup` still runs. The policy must be
  explicit and documented, not an unconditional hardcode.
- On 9654 the skip saves the cgroup write but adds 10-14ms to
  `new_from_snapshot` in the VMM device-creation phase; `auto` is not a
  universal win.
- `always` remains the default so existing deployments do not silently change
  placement/accounting; `auto` is opt-in and should be enabled after A/B on
  the target platform.

## Measured effect (20s-settle template, fresh sandbox, single resume)

Client resume wall, two interleaved rounds per arm:

| arm | 950 | 9654 |
|---|---:|---:|
| early join | 100.8, 110.5 (mean 105.7) | 102.5, 101.8 (mean 102.2) |
| skip all | 67.4, 70.6 (mean 69.0) | 91.6, 93.1 (mean 92.4) |
| skip cpu only | 114.8, 122.3 (mean 118.6) | 100.0, 87.9 (mean 94.0) |

- 950: −36.7ms (−35%), matching the 36.0ms first-write stage; other stages
  unchanged.
- 9654: −9.8ms, because the 19.2ms cgroup saving is partly offset by the
  VMM `new_from_snapshot` penalty; net ~10ms.
- Skip-cpu-only is worse on 950: the memory first write pays the same cost.

## Safety

- If the sandbox cgroup has an effective cpuset, use `always`; the late move
  cannot reliably migrate already-faulted, pinned guest pages.
- `auto`/`never` leave a ~50-100ms window in which the shim/VMM is outside
  the sandbox cpu/memory cgroup. Quota/accounting/OOM attribution for that
  window is deferred to `setCgroup`. Verify the final
  `/proc/<shim>/cgroup` and decide whether the window is acceptable.
- The late `setCgroup` must remain enabled; it is the correctness fallback.
- `disable_host_cgroup` is unrelated: it only controls host limit setting,
  not membership or this policy.

## Build and test

Cubelet changes only; the shim remains the instrumentation tree.

```bash
cd Cubelet
go test ./services/cubebox -run 'HostCgroup|EarlyJoin' -count=1
go build ./...
```

Full data/analysis: `0.7版本的性能优化/38-resume阶段分解与cgroup首写-20261030.md`.
