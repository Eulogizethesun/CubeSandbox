// Copyright (c) 2024 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

//! Join the sandbox process into the pool cgroup that Cubelet assigned for
//! it, BEFORE the VMM starts: a snapshot restore's first-touch pages should
//! be allocated under the target group's cpuset.mems. Cubelet's late
//! setCgroup remains the idempotent fallback.

use std::path::Path;

pub const CGROUP_ROOT: &str = "/sys/fs/cgroup";

/// Only Cubelet's pool paths are accepted: a foreign or malformed
/// cube.sandbox_cgroup_path annotation must not steer this process into an
/// arbitrary cgroup.
fn valid_pool_path(path: &str) -> bool {
    let p = path.trim();
    (p.starts_with("/cube_sandbox/sandbox/") || p.starts_with("/cube_sandbox_v2/sandbox/numa"))
        && !p.contains("/../")
        && !p.ends_with("/..")
}

/// Join this process into the pool cgroup at `path` (relative to the cgroup
/// root, as produced by Cubelet's MakeCgroupPathByID).
pub fn join(path: &str) {
    if !valid_pool_path(path) {
        eprintln!("invalid pool cgroup path {path:?}; skip early join");
        return;
    }
    let pid = std::process::id().to_string();
    let root = Path::new(CGROUP_ROOT);
    if root.join("cgroup.controllers").exists() {
        // cgroup v2: one tree, one write covers all controllers.
        write_procs(&root.join(path.trim_start_matches('/')), &pid);
        return;
    }
    // cgroup v1: one tree per controller; the group exists only in the
    // trees Cubelet attached at creation — write those, skip the rest.
    for ctrl in ["cpu", "memory", "cpuset"] {
        let dir = root.join(ctrl).join(path.trim_start_matches('/'));
        if dir.is_dir() {
            write_procs(&dir, &pid);
        }
    }
}

fn write_procs(dir: &Path, pid: &str) {
    let procs = dir.join("cgroup.procs");
    if let Err(e) = std::fs::write(&procs, pid) {
        eprintln!("write {}: {e}", procs.display());
    }
}
