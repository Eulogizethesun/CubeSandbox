// Copyright (c) 2024 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

//! Join the sandbox process into the pool cgroup that cubelet assigned for
//! it, BEFORE the VMM starts: the snapshot-restore first-touch pages must be
//! allocated under the target group's cpuset.mems, and cubelet's late move
//! cannot migrate pages that already landed elsewhere.
//!
//! Contract: only pool paths are accepted; best-effort — failures are reported on stderr and
//! cubelet's late `setCgroup` stays as the idempotent fallback.

use std::path::Path;
use std::time::Instant;

pub const CGROUP_ROOT: &str = "/sys/fs/cgroup";

/// Only cubelet's pool paths are accepted: a foreign or malformed
/// cube.sandbox_cgroup_path annotation must not steer this process into an
/// arbitrary cgroup.
fn valid_pool_path(path: &str) -> bool {
    let p = path.trim();
    (p.starts_with("/cube_sandbox/sandbox/")
        || p.starts_with("/cube_sandbox_v2/sandbox/numa"))
        && !p.contains("/../")
        && !p.ends_with("/..")
}

/// Join this process into the pool cgroup at `path` (relative to the cgroup
/// root, as produced by cubelet's MakeCgroupPathByID). Returns a measurement
/// summary (per-controller write microseconds) for the caller's log.
pub fn join(path: &str) -> String {
    if !valid_pool_path(path) {
        eprintln!("invalid pool cgroup path {path:?}; skip early join");
        return "path=invalid".to_string();
    }
    let pid = std::process::id().to_string();
    let root = Path::new(CGROUP_ROOT);
    let tasks = std::fs::read_dir("/proc/self/task")
        .map(|it| it.count())
        .unwrap_or(0);
    if root.join("cgroup.controllers").exists() {
        // cgroup v2: one tree, one write covers all controllers.
        let total = Instant::now();
        let t = Instant::now();
        let ok = write_procs(&root.join(path.trim_start_matches('/')), &pid);
        return format!(
            "v2 ok={} us={} tasks={} total_us={}",
            ok,
            t.elapsed().as_micros(),
            tasks,
            total.elapsed().as_micros()
        );
    }
    // cgroup v1: one tree per controller; the group exists only in the
    // trees cubelet attached at creation — write those, skip the rest
    // (same semantics as the containerd library's Load).
    let total = Instant::now();
    let mut out = String::new();
    for ctrl in ["cpu", "memory", "cpuset"] {
        let dir = root.join(ctrl).join(path.trim_start_matches('/'));
        let t = Instant::now();
        let exists = dir.is_dir();
        let exists_us = t.elapsed().as_micros();
        if exists {
            let t = Instant::now();
            let ok = write_procs(&dir, &pid);
            out.push_str(&format!(
                " {ctrl}=ok{}write_us{}_is_dir_us{}",
                ok as u8,
                t.elapsed().as_micros(),
                exists_us
            ));
        } else {
            out.push_str(&format!(" {ctrl}=absent_is_dir_us{exists_us}"));
        }
    }
    format!("v1 tasks={} total_us={}{}", tasks, total.elapsed().as_micros(), out)
}

fn write_procs(dir: &Path, pid: &str) -> bool {
    let procs = dir.join("cgroup.procs");
    match std::fs::write(&procs, pid) {
        Ok(()) => true,
        Err(e) => {
            eprintln!("write {}: {e}", procs.display());
            false
        }
    }
}
