// Copyright (c) 2024 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

use std::time::Instant;

use super::perf_trace::{self, PerfTrace};
use super::{Log, StatRet};

pub const ACT_CREATE: &str = "Create";
pub const ACT_DELETE: &str = "Delete";
pub const ACT_START: &str = "Start";
pub const ACT_WAIT: &str = "Wait";
pub const ACT_PAUSE: &str = "Pause";
pub const ACT_RESET_VM: &str = "ResetVm";
/*
    CalleeActionCreatePodSandbox   = "CreatePodSandbox"
    CalleeActionCreatePodContainer = "CreatePodContainer"
    CalleeActionStartVm            = "StartVm"
    CalleeActionBootVmm            = "BootVm"
    CalleeActionCreateVm           = "CreateVm"
    CalleeActionRestoreVm          = "RestoreVm"
    CalleeActionLaunchVmm          = "LaunchVmm"
    CalleeActionCreateSandbox      = "CreateSandbox"
    CalleeActionCreateContainer    = "CreateContainer"

    CalleeActionFsSharePrepare = "FsSharePrepare"
    CalleeActionCubeHCreate    = "CubeHCreate"
    CalleeActionCubeHLaunch    = "CubeHLaunch"
    CalleeActionHotplugDisk    = "HotplugDisk"
    CalleeActionHotplugNet     = "HotplugNet"

    CalleeActionStart    = "Start"
    CalleeActionDelete   = "Delete"
    CalleeActionKill     = "Kill"
    CalleeActionWait     = "Wait"
    CalleeActionShutDown = "Shutdown"
*/
pub const CALLEE_ACT_CREATE_POD_SANDBOX: &str = "CreatePodSandbox";
pub const CALLEE_ACT_CREATE_POD_CONTAINER: &str = "CreatePodContainer";
pub const CALLEE_ACT_LAUNCH_VMM: &str = "LaunchVmm";
pub const CALLEE_ACT_BOOT_VM: &str = "BootVm";
pub const CALLEE_ACT_CREATE_VM: &str = "CreateVm";
pub const CALLEE_ACT_RESTORE_VM: &str = "RestoreVm";
pub const CALLEE_ACT_CREATE_SANDBOX: &str = "CreateSandbox";
pub const CALLEE_ACT_CREATE_CONTAINER: &str = "CreateContainer";

pub const CALLEE_ACT_RESET_VM: &str = "ResetVm";
pub const CALLEE_ACT_PAUSE_TO_SNAPSHOT: &str = "PauseToSnapshot";

pub const CALLEE_ACT_DEL_CONTAINER: &str = "DeleteContainer";

pub const CALLEE_ACT_AGENT: &str = "Agent";

pub const CALLEE_SHIM: &str = "Shim";
pub const CALLEE_CH: &str = "Ch";
pub const CALLEE_AGENT: &str = "Agent";

pub struct StatDefer {
    cid: String,
    callee: String,
    action: String,
    callee_act: String,
    start: Instant,
    operation_id: Option<u64>,
    ret: StatRet,
    log: Log,
    loged: bool,
}
/*
       container_id: String,
       caller: String,
       callee: String,
       action: String,
       callee_action: String,
       ret: StatRet,
       cost: u64,
*/
impl StatDefer {
    pub fn new(cid: String, callee: String, action: String, callee_act: String, log: Log) -> Self {
        // Initialize the clock before capturing the operation's exact start.
        let operation_id = perf_trace::new_operation();
        StatDefer {
            cid,
            callee,
            action,
            callee_act,
            start: Instant::now(),
            operation_id,
            ret: StatRet::Err,
            log,
            loged: false,
        }
    }

    pub fn set_ok(&mut self) {
        self.ret = StatRet::Ok;
    }

    pub fn set_callee_act(&mut self, act: String) {
        self.callee_act = act;
    }

    pub fn stat(&mut self) {
        let duration = self.start.elapsed();
        let perf_trace = self
            .operation_id
            .map(|id| PerfTrace::capture(id, self.start, duration, "span"));
        self.log.stat(
            self.cid.clone(),
            self.callee.clone(),
            self.action.clone(),
            self.callee_act.clone(),
            self.ret.clone(),
            duration.as_millis(),
            perf_trace,
        );
        self.loged = true
    }

    /// Emits a cumulative checkpoint from this operation's start, not a child span.
    pub fn perf_marker(&self, marker: &str) {
        self.perf_marker_in("TaskCreate", marker);
    }

    /// Cumulative checkpoint with an explicit stage prefix.
    pub fn perf_marker_in(&self, group: &str, marker: &str) {
        if let Some(id) = self.operation_id {
            let elapsed = self.start.elapsed();
            self.log.stat(
                self.cid.clone(),
                self.callee.clone(),
                self.action.clone(),
                format!("{}.{}", group, marker),
                StatRet::Ok,
                elapsed.as_millis(),
                Some(PerfTrace::capture(id, self.start, elapsed, "cumulative")),
            );
        }
    }
}

/// Emits a PauseToSnapshot checkpoint when the optional measurement scope is present.
pub fn pause_marker(stat: Option<&StatDefer>, marker: &str) {
    if let Some(s) = stat {
        s.perf_marker_in("PauseToSnapshot", marker);
    }
}

/// Emits a standalone span for a scope that has no StatDefer (agent RPCs, for
/// example). The caller supplies the start instant; the span ends now.
pub fn emit_span(
    log: &Log,
    cid: &str,
    callee: &str,
    action: &str,
    callee_action: &str,
    start: Instant,
    ok: bool,
) {
    let elapsed = start.elapsed();
    let operation_id = perf_trace::new_operation();
    let trace = operation_id.map(|id| PerfTrace::capture(id, start, elapsed, "span"));
    log.stat(
        cid.to_string(),
        callee.to_string(),
        action.to_string(),
        callee_action.to_string(),
        if ok { StatRet::Ok } else { StatRet::Err },
        elapsed.as_millis(),
        trace,
    );
}

impl Drop for StatDefer {
    fn drop(&mut self) {
        if !self.loged {
            self.stat();
        }
    }
}
