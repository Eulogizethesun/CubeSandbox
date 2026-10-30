# Functional early-cgroup skip experiment

This tree is the **functional** companion to `CubeSandbox-pauseprof-resume`
(instrumentation only). It keeps all resume instrumentation and adds two
file-gated early-join arms so the cgroup's first `cgroup.procs` write can be
A/B measured. **Not for production.**

## Arms

Both switches are files, checked at early-join time; default off.

- `/tmp/cube_skip_early_cgroup`: when the cpuset tree has no group for the
  sandbox, skip the whole early join (cpu + memory + cpuset) and let
  Cubelet's late `setCgroup` place the process. The late move still runs;
  verify `/proc/<shim>/cgroup` after resume before trusting an arm.
- `/tmp/cube_skip_early_cpu`: skip only the cpu write; memory still joins.
  This arm exists to show the first-write cost is not controller-specific:
  the memory write becomes the slow one.

## What the instrumentation showed

The expensive part is the **first `cgroup.procs` write by a fresh shim
process**, not a controller and not the cpuset check:

- 950: first write 16-75ms; 9654: 7-25ms.
- The same process's next controller write is 20-60us.
- With `skip_early_cpu`, 950's memory first write becomes 20.6/57.3ms.
- With `skip_early_cgroup`, neither write happens (cgroup stage ~0.04ms).

## A/B/C result

Fresh sandbox, single pause/resume, 20s-settle template, two interleaved
rounds per arm; client wall milliseconds:

| arm | 950 | 9654 |
|---|---:|---:|
| A early join | 100.8, 110.5 (mean 105.7) | 102.5, 101.8 (mean 102.2) |
| B `skip_early_cgroup` | 67.4, 70.6 (mean 69.0) | 91.6, 93.1 (mean 92.4) |
| C `skip_early_cpu` | 114.8, 122.3 (mean 118.6) | 100.0, 87.9 (mean 94.0) |

- 950: A→B −36.7ms, matching the 36.0ms cgroup mean; later agent, container
  and probe stages are unchanged.
- 9654: A→B −9.8ms; the 19.2ms cgroup saving is offset by a 10-14ms
  `new_from_snapshot` penalty (VMM device creation), so the net is ~10ms.
- C is worse on 950 because the memory first write pays the same cost.

## Safety conditions

Only valid when **all** of these hold:

1. the cpuset tree has no group for the sandbox (no NUMA first-touch
   guarantee to preserve);
2. `disable_host_cgroup=true` (host CPU/memory limits are off) or the
   restore-window overrun is acceptable;
3. Cubelet's late `setCgroup` still runs and is verified;
4. the 50-100ms window without sandbox cgroup quota/accounting is accepted.

If cpuset exists, or host limits are enabled, do not skip the early join.
The cgroup cost is the **first membership write**, not the limit-setting
path; `disable_host_cgroup` does not remove it.

## Build and measure

Same build as `PAUSEPROF.md`. Deploy the shim to the path Cubelet actually
executes (`/data/cubelet/root/component_versions/cube-shim/<ver>/bin/`), not
only `/usr/local/services/.../cube-shim/bin`. Remove the switch files and
restore the production binaries after the experiment.

Full data and analysis: `0.7版本的性能优化/38-resume阶段分解与cgroup首写-20261030.md`.
