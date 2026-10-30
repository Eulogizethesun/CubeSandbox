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
cpu=ok1write_us.. memory=.. cpuset=absent..)`). The expensive part is the
**first `cgroup.procs` write by the shim process**, not a controller: the
cpu write is 16-75ms on 950 and 7-25ms on 9654, while the next controller
write in the same process is 20-60us. This tree only measures that write;
the functional early-join skip experiment lives in the separate
`CubeSandbox-cgroup-skip-20261030` tree, not here.

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
