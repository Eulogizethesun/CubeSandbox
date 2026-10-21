// Copyright (c) 2026 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0

//! Measurement-build monotonic timing records for stat entries (default on;
//! set CUBE_PERF_TRACE=0 to disable).
//!
//! Ported from the cubesandbox-startup-profiler observation patch. One
//! process-local clock and one operation id per StatDefer scope; records carry
//! nanosecond endpoints so the offline analyzer can rebuild intervals that the
//! integer-millisecond CostTime cannot express.

use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::OnceLock;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use serde::{Deserialize, Serialize};

pub const ENV_PERF_TRACE: &str = "CUBE_PERF_TRACE";
static ENABLED: OnceLock<bool> = OnceLock::new();
static CLOCK: OnceLock<TraceClock> = OnceLock::new();
static NEXT_OPERATION: AtomicU64 = AtomicU64::new(1);
static DROPPED_RECORDS: AtomicU64 = AtomicU64::new(0);

struct TraceClock {
    origin: Instant,
    id: String,
}

/// Measurement build: tracing is on by default so a plain binary copy needs no
/// environment plumbing through containerd; set `CUBE_PERF_TRACE=0` to disable.
pub fn enabled() -> bool {
    *ENABLED.get_or_init(|| std::env::var(ENV_PERF_TRACE).as_deref() != Ok("0"))
}

fn clock() -> &'static TraceClock {
    CLOCK.get_or_init(|| TraceClock {
        origin: Instant::now(),
        id: format!(
            "{}:{}",
            std::process::id(),
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap_or_default()
                .as_nanos()
        ),
    })
}

/// Returns an operation identity after initializing its process-local clock.
pub fn new_operation() -> Option<u64> {
    if !enabled() {
        return None;
    }
    clock();
    Some(NEXT_OPERATION.fetch_add(1, Ordering::Relaxed))
}

pub fn record_drop() {
    DROPPED_RECORDS.fetch_add(1, Ordering::Relaxed);
}

#[derive(Debug, Serialize, Deserialize)]
#[serde(rename_all = "PascalCase")]
pub struct PerfTrace {
    pub schema_version: u8,
    pub clock_id: String,
    pub operation_id: u64,
    pub kind: String,
    pub start_mono_ns: u128,
    pub end_mono_ns: u128,
    pub duration_ns: u128,
    pub duration_us: u128,
    pub dropped_records_before: u64,
}

impl PerfTrace {
    pub fn capture(operation_id: u64, start: Instant, elapsed: Duration, kind: &str) -> Self {
        let clock = clock();
        let start_mono_ns = start.duration_since(clock.origin).as_nanos();
        Self {
            schema_version: 1,
            clock_id: clock.id.clone(),
            operation_id,
            kind: kind.to_owned(),
            start_mono_ns,
            end_mono_ns: start_mono_ns + elapsed.as_nanos(),
            duration_ns: elapsed.as_nanos(),
            duration_us: elapsed.as_micros(),
            dropped_records_before: DROPPED_RECORDS.load(Ordering::Relaxed),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn captures_submicrosecond_duration_without_rounding_the_interval() {
        clock();
        let trace = PerfTrace::capture(7, Instant::now(), Duration::from_nanos(999), "span");
        assert_eq!(trace.end_mono_ns - trace.start_mono_ns, 999);
        assert_eq!(trace.duration_ns, 999);
        assert_eq!(trace.duration_us, 0);
    }

    #[test]
    fn cumulative_markers_share_operation_clock_and_start() {
        clock();
        let start = Instant::now();
        let first = PerfTrace::capture(42, start, Duration::from_nanos(101), "cumulative");
        let second = PerfTrace::capture(42, start, Duration::from_nanos(180), "cumulative");
        assert_eq!(first.clock_id, second.clock_id);
        assert_eq!(first.operation_id, second.operation_id);
        assert_eq!(first.start_mono_ns, second.start_mono_ns);
        assert_eq!(second.end_mono_ns - first.end_mono_ns, 79);
    }
}
