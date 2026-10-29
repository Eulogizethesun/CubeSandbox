# pauseprof pause-stage instrumentation (v0.7.1 + A)

This branch is the instrumented tree used for the C1/C10 pause stage
breakdown in https://github.com/Eulogizethesun/CubeSandbox/issues/35.

- Base: `e1ec7419` (v0.7.1 + MR #43/A).
- **Diagnostic only; not for production.** The changes add perf-trace
  checkpoints and VMM phase logs. Runtime spike switches default off.

## Instrumented files

- CubeShim: `log/perf_trace.rs` (new), `log/mod.rs`,
  `log/stat_defer.rs`, `sandbox/sb.rs`,
  `service/{srv,task_srv,update_ext}.rs`, `common/utils.rs`.
- VMM: `hypervisor/vmm/src/lib.rs` (freeze/snapshot/stop/total),
  `hypervisor/vmm/src/memory_manager.rs` (`mem_send` plus
  `mem_split` open/bitmap/write/ranges/bytes).
- Cubelet: `pkg/perftrace/trace.go` (new),
  `services/cubebox/{pause_cow,update,service,cube_container_create}.go`,
  `services/cubebox/spike.go` (new).
- CubeMaster: `pkg/service/sandbox/sandbox_update.go` (entry marker).

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
