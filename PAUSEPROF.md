# pauseprof pause/resume stage instrumentation (v0.7.1 + A)

This branch is the instrumented tree used for the C1/C10 pause stage
breakdown in https://github.com/Eulogizethesun/CubeSandbox/issues/35 and
for the Resume (`cubelet.Create` restore RPC) breakdown.

- Base: `e1ec7419` (v0.7.1 + MR #43/A).
- **Diagnostic only; not for production.** The changes add perf-trace
  checkpoints and VMM phase logs. Runtime spike switches default off.

## Instrumented files

- CubeShim: `log/perf_trace.rs` (new), `log/mod.rs`,
  `log/stat_defer.rs`, `sandbox/sb.rs`,
  `service/{srv,task_srv,update_ext}.rs`, `common/utils.rs`.
- VMM: `hypervisor/vmm/src/lib.rs` (pause freeze/snapshot/stop/total;
  resume config/state/new_from_snapshot/vm_restore/vm_resume/total),
  `hypervisor/vmm/src/memory_manager.rs` (`mem_send`, `mem_split`
  open/bitmap/write/ranges/bytes; resume `mem_open`/`mem_state`/`mem_new`).
- Cubelet: `pkg/perftrace/trace.go` (new),
  `services/cubebox/{pause_cow,update,service,cube_container_create}.go`,
  `services/cubebox/spike.go` (new).
- CubeMaster: `pkg/service/sandbox/sandbox_update.go` (pause entry marker),
  `pkg/service/sandbox/sandbox_resume_pause.go` (resume phases).

## Resume path

The resume flow is Master `sandbox_resume_pause.go` -> Cubelet `Create`
(thin Create) -> `engine.Create` -> containerd `NewTask` -> shim
`CreateTask` -> VMM `vm_restore`. Each layer emits its own clock:

- Master: `cubeprof master resume phase=<phase> us=<delta> total_us=<...>`
  with `phase=cubelet_create_start` before the RPC and
  `phase=cubelet_create` after it (the RPC interval is the delta between
  the two).
- Cubelet (needs `CUBE_PERF_TRACE=1`): one `CUBE_PERF_TRACE {json}` line
  per Create request with cumulative checkpoints:
  `Resume.CrossNodeRestore`, `Resume.ExpandPausePackage`,
  `Resume.LifecycleLock`, `Engine.CallStart`, `Engine.Enter`,
  `Engine.Tombstone`, `Engine.PreContainers`, `Workflow.Start`,
  `Workflow.Runtime`, `Workflow.Components`,
  `Workflow.CubeboxContainer`, `Workflow.Options`, `Workflow.Specs`,
  `Container.NewContainer`, `Container.NewTask`, `Container.TaskWait`,
  `Container.TaskStart`, `Probe.ContainerStart/Done`,
  `Probe.Health/Done`, `Probe.PostDone`, `Workflow.Containers`,
  `Workflow.EnvdInit`, `Workflow.Done`, `Engine.CallDone`,
  `Engine.Containers`, `Engine.Cgroup`, `Engine.Done`,
  `Response.Ready`.
- Shim: CreateTask stat with cumulative `callee_action` checkpoints
  `TaskCreate.LoadSpec`, `AcquireSandboxLock`, `CheckSandboxState`,
  `InitSandbox`, `TaskCreate.CreateSandbox.Cgroup`, `StartVm.LaunchVmm`,
  `Restore.Meta`, `Restore.VmmLock`, `Restore.VmmDone`, `Restore.Pmem`,
  `StartVm.RestoreVm`, `CreateSandbox.NotifySnapshot`,
  `AgentConnect.Enter/Handshake/Reset`, `CreateSandbox.AgentConnect`,
  `CreateSandbox.AddDevice`, `CreateSandbox.Storages`,
  `CreateSandbox.AgentCreateSandbox`, `CreateSandbox.WatchMonitor`,
  `StartSandbox`, `CreateContainer.New`, `CreateContainer.AgentRpc`,
  `CreateContainer.Stored`, `CreateContainer`, `PublishTaskEvent`.
  The `Start` RPC and the agent CreateContainer/StartContainer calls have
  their own stats (`StartContainer.*`, `CreateContainer.*`).
- VMM: `cubeprof resume phase=config|state|pre_new_from_snapshot|
  new_from_snapshot|vm_restore|vm_resume|total` plus
  `mem_open|mem_state|mem_new|mem_fill`.

Nesting gives the attribution without cross-process clock alignment:
Master `cubelet_create` ~ Cubelet request > `Engine.Call*` >
`Container.NewTask` (shim CreateTask) > `Restore.Vmm*` (VMM restore).

`host_cgroup::join` also returns a per-controller write summary (the shim
log line `host cgroup ... join attempted (v1 tasks=.. total_us=..
cpu=ok1write_us.. memory=.. cpuset=absent..)`). The early join's cpu-controller
write is the largest resume stage on the arm64 node when it is warm-cold:
0.05ms on a pre-used pool group but 16-75ms on an unused one. Two file
switches are measurement arms only, both default off:

- `/tmp/cube_skip_early_cgroup`: when the cpuset tree has no group for the
  sandbox, skip the whole early join (cpu+memory+cpuset) and let cubelet's
  late `setCgroup` place the process. The late move still runs; verify
  `/proc/<shim>/cgroup` after resume before trusting an arm.
- `/tmp/cube_skip_early_cpu`: skip only the cpu write (memory/cpuset stay).

Resume breakdown (fresh sandbox, single pause/resume, 20s-settle template;
two interleaved rounds per arm; values are client wall milliseconds):

| arm | 950 | 9654 |
|---|---:|---:|
| early join (baseline) | 100.8, 110.5 | 102.5, 101.8 |
| skip all (`skip_early_cgroup`) | 67.4, 70.6 | 91.6, 93.1 |
| skip cpu only (`skip_early_cpu`) | 114.8, 122.3 | 100.0, 87.9 |

On 950 the skip-all arm removes the 16-75ms cpu write and also shortens the
later agent/container/probe stages (joining the pool cgroup before restore
appears to throttle the control-plane work); the late cubelet move still puts
the finished sandbox into its cgroup. On 9654 the saved join time is offset by
a 10-14ms `new_from_snapshot` penalty in the skip arm, so the net is ~10ms.
This is a platform-dependent optimization, not a universal win; the file
switches are diagnostic arms, not a production recommendation.

## Resume analyzer

`scripts/pauseprof/resume_trace_analyze.py <sandbox-id>` reads the four logs
above and prints a SUMMARY (Cubelet request total, NewTask, probe, shim
TaskCreate stages, VMM phases, Master phases, and the Cubelet-NewTask vs
shim-TaskCreate cross-check) followed by the raw cumulative checkpoints. Set
`CUBE_LOG_ROOT` when the logs are not under `/data/log`.

## Build and measure

- Shim: `cd CubeShim && cargo build --release --locked`.
- Cubelet: `IN_CUBE_SANDBOX_BUILDER=1 make cubecow-sdk`,
  `(cd CubeNet/cubevs && make gen)`,
  `cd Cubelet && go build ./... && make build`.
- Master: `cd CubeMaster && make cubemaster`.
- Shim trace defaults on; Cubelet needs `CUBE_PERF_TRACE=1`.
- Deploy the three binaries, restart the services, then capture with
  the pauseprof `capture.py` and analyze with
  `profiler.py analyze --profile pause`.
- VMM lines are `cubeprof pause phase=...`; `phase=stop` is
  cumulative, so the stop interval is `stop - snapshot`.
- Restore the production binaries after measurement.

Tool: https://gitcode.com/woodenarow/cubesandbox-startup-profiler
(branch `pauseresume`).
