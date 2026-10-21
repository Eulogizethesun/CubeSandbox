// Copyright (c) 2024 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

use containerd_shim::asynchronous::run as shim_run;
use containerd_shim::{parse, Config};
use containerd_shim_cube_rs::common;
use containerd_shim_cube_rs::service::Service;

use std::ffi::OsString;
use std::fs::File;
use std::io::{self, Write};
use tokio::runtime::Builder;
//const SHIM_VERSION: &str = env!("GIT_COMMIT_INFO");
//const CH_VERSION: &str = env!("CH_GIT_COMMIT_INFO");
fn main() {
    let c = Config {
        no_reaper: true,
        no_setup_logger: true,
        no_sub_reaper: true,
        ..Default::default()
    };

    let mut thread_num = 1;
    let os_args: Vec<_> = std::env::args_os().collect();
    if is_version_request(&os_args[1..]) {
        print_version();
        return;
    }
    if is_runtime_info_request(&os_args[1..]) {
        if let Err(err) = handle_runtime_info_request() {
            eprintln!("handle runtime info request failed: {err}");
            unsafe {
                libc::exit(1);
            }
        }
        return;
    }
    let flags = parse(&os_args[1..]).expect("Invalid params");
    if flags.version {
        print_version();
        return;
    }
    if flags.action.is_empty() {
        thread_num = 2;
        set_process();
    }

    let runtime = Builder::new_multi_thread()
        .worker_threads(thread_num)
        .enable_all()
        .build()
        .unwrap();
    runtime.block_on(shim_run::<Service>("io.containerd.cube.rs", Some(c)));
}

fn is_version_request(args: &[OsString]) -> bool {
    args.iter().any(|arg| {
        arg.to_str()
            .map(|value| matches!(value, "-v" | "-version" | "--version"))
            .unwrap_or(false)
    })
}

fn print_version() {
    println!(
        "containerd-shim-cube-rs {} ({}) built at {}",
        common::SHIM_VERSION,
        common::SHIM_COMMIT,
        common::SHIM_BUILD_TIME
    );
}

fn is_runtime_info_request(args: &[OsString]) -> bool {
    args.iter().any(|arg| {
        arg.to_str()
            .map(|value| matches!(value, "-info" | "--info"))
            .unwrap_or(false)
    })
}

fn handle_runtime_info_request() -> io::Result<()> {
    // containerd v2 expects shim -info to print a RuntimeInfo protobuf.
    // An empty message is sufficient for CubeShim because we do not expose
    // extra runtime capabilities through this probe yet.
    io::copy(&mut io::stdin().lock(), &mut io::sink())?;
    io::stdout().flush()?;
    Ok(())
}

fn set_process() {
    //core dump filter
    let mut filter_file = File::options()
        .write(true)
        .open("/proc/self/coredump_filter")
        .expect("open coredump_filter failed");
    filter_file
        .write_all("0x33".as_bytes())
        .expect("write coredump_filter failed");

    //setrlimit(Resource::CORE, coredump_limit, coredump_limit).expect("set coredump limit failed");
    let coredump_limit = 1024 * 1024 * 1024 * 2;
    let core = libc::rlimit {
        rlim_cur: coredump_limit,
        rlim_max: coredump_limit,
    };
    unsafe {
        if libc::setrlimit(libc::RLIMIT_CORE, &core) < 0 {
            eprintln!("set rlimit core failed:{}", io::Error::last_os_error());
            libc::exit(1);
        }

        let mut nofile = libc::rlimit {
            rlim_cur: 0,
            rlim_max: 0,
        };

        if libc::getrlimit(libc::RLIMIT_NOFILE, &mut nofile) < 0 {
            eprintln!("get rlimit nofile failed:{}", io::Error::last_os_error());
            libc::exit(1);
        }

        if nofile.rlim_cur < nofile.rlim_max {
            nofile.rlim_cur = nofile.rlim_max;
            if libc::setrlimit(libc::RLIMIT_NOFILE, &nofile) < 0 {
                eprintln!("set rlimit nofile failed:{}", io::Error::last_os_error());
                libc::exit(1);
            }
        }

        // Create dummy socket and dup it to large fd, so that we could trigger
        // expand_files in kernel. And this tricky work will avoid later multi
        // thread try to invoke expand_files at same time, there will be lock
        // contention in expand_files.
        // SAFETY: FFI call
        let dummy = libc::socket(libc::AF_UNIX, libc::SOCK_STREAM | libc::SOCK_CLOEXEC, 0);
        if dummy > 0 {
            libc::dup2(dummy, 512);
        }
    }

    reset_affinity_to_housekeeping();
}

/// Compute the housekeeping CPU set from the two sysfs lists: online minus
/// isolated, capped to what `cpu_set_t` can represent (CPU_SETSIZE bits).
fn housekeeping_cpus(online: &str, isolated: &str) -> Vec<usize> {
    let parse = |s: &str| -> std::collections::BTreeSet<u32> {
        let mut set = std::collections::BTreeSet::new();
        for token in s.split(',') {
            let token = token.trim();
            let (a, b) = match token.split_once('-') {
                Some((a, b)) => (a, b),
                None => {
                    if let Ok(cpu) = token.parse::<u32>() {
                        set.insert(cpu);
                    }
                    continue;
                }
            };
            if let (Ok(a), Ok(b)) = (a.trim().parse::<u32>(), b.trim().parse::<u32>()) {
                if a <= b {
                    for cpu in a..=b {
                        set.insert(cpu);
                    }
                }
            }
        }
        set
    };
    let online = parse(online);
    let isolated = parse(isolated);
    online
        .difference(&isolated)
        .filter(|cpu| **cpu < libc::CPU_SETSIZE as u32)
        .map(|cpu| *cpu as usize)
        .collect()
}

/// Reset the process CPU affinity mask to the housekeeping set: all online
/// CPUs minus the isolated ones (`/sys/devices/system/cpu/isolated`, empty
/// unless the host booted with isolcpus=).
///
/// cubelet embeds containerd and forks us, so this process inherits whatever
/// mask cubelet was launched with (taskset/numactl pinning of the control
/// plane) — without the reset, every shim thread and VMM vCPU would be
/// pinned to the control plane's CPUs for the sandbox's whole life. cgroup
/// cpuset cannot undo an inherited mask (it can only narrow it), so the
/// reset must happen here, while the process is still single-threaded:
/// threads and forks started later inherit the corrected mask.
///
/// Best-effort by contract: any failure just warns and keeps the inherited
/// mask (the pre-fix behavior); it must never block shim startup.
fn reset_affinity_to_housekeeping() {
    let online_raw = match std::fs::read_to_string("/sys/devices/system/cpu/online") {
        Ok(s) => s,
        Err(e) => {
            eprintln!("read /sys/devices/system/cpu/online failed: {e}; keep inherited affinity");
            return;
        }
    };
    // A missing isolated file means nothing is isolated — subtract nothing.
    let isolated_raw = std::fs::read_to_string("/sys/devices/system/cpu/isolated")
        .unwrap_or_default();
    let target = housekeeping_cpus(&online_raw, &isolated_raw);
    if target.is_empty() {
        eprintln!("housekeeping cpu set is empty; keep inherited affinity");
        return;
    }
    unsafe {
        let mut set: libc::cpu_set_t = std::mem::zeroed();
        libc::CPU_ZERO(&mut set);
        for cpu in &target {
            libc::CPU_SET(*cpu, &mut set);
        }
        if libc::sched_setaffinity(0, std::mem::size_of::<libc::cpu_set_t>(), &set) != 0 {
            eprintln!(
                "sched_setaffinity to housekeeping set failed: {}; keep inherited affinity",
                io::Error::last_os_error()
            );
        }
    }
}

#[cfg(test)]
mod tests {
    use super::{housekeeping_cpus, reset_affinity_to_housekeeping};

    #[test]
    fn ranges_and_singles() {
        assert_eq!(
            housekeeping_cpus("0-3,8", ""),
            vec![0, 1, 2, 3, 8],
            "ranges expand and singles append, sorted"
        );
    }

    #[test]
    fn kernel_format_with_whitespace() {
        // sysfs files end with a newline; interior whitespace is tolerated.
        assert_eq!(housekeeping_cpus("0-1\n", "").len(), 2);
        assert_eq!(housekeeping_cpus(" 0 - 2 , 5 ", "").len(), 4);
    }

    #[test]
    fn empty_and_garbage_degrade_to_empty() {
        assert!(housekeeping_cpus("", "").is_empty());
        assert!(housekeeping_cpus("\n", "").is_empty());
        assert!(housekeeping_cpus(",,,x,y-", "").is_empty());
        // Reversed range is malformed, not an expansion.
        assert!(housekeeping_cpus("5-3", "").is_empty());
    }

    #[test]
    fn isolated_cpus_are_subtracted() {
        assert_eq!(
            housekeeping_cpus("0-7", "2-3"),
            vec![0, 1, 4, 5, 6, 7],
            "isolcpus cores are excluded from the housekeeping set"
        );
        // Isolating everything leaves nothing — the caller must detect and
        // keep the inherited mask instead of writing an empty set.
        assert!(housekeeping_cpus("0-7", "0-7").is_empty());
    }

    #[test]
    fn cpus_beyond_cpuset_size_are_dropped() {
        // cpu_set_t holds CPU_SETSIZE (1024) bits; higher ids cannot be
        // represented and must not leak into the syscall argument.
        assert_eq!(
            housekeeping_cpus("1023-1025,5", ""),
            vec![5, 1023]
        );
    }

    #[test]
    fn reset_effective_mask_stays_within_housekeeping() {
        // End-to-end over the real sysfs + syscall path: after the reset,
        // every CPU the thread may run on must come from the housekeeping
        // set computed independently above.
        let online = std::fs::read_to_string("/sys/devices/system/cpu/online").unwrap_or_default();
        let isolated =
            std::fs::read_to_string("/sys/devices/system/cpu/isolated").unwrap_or_default();
        let expected = housekeeping_cpus(&online, &isolated);
        assert!(!expected.is_empty(), "no housekeeping cpus on this machine");

        reset_affinity_to_housekeeping();

        unsafe {
            let mut set: libc::cpu_set_t = std::mem::zeroed();
            assert_eq!(
                libc::sched_getaffinity(0, std::mem::size_of::<libc::cpu_set_t>(), &mut set),
                0,
                "sched_getaffinity failed"
            );
            for cpu in 0..libc::CPU_SETSIZE {
                if libc::CPU_ISSET(cpu as usize, &set) {
                    assert!(
                        expected.contains(&(cpu as usize)),
                        "cpu {} is outside the housekeeping set",
                        cpu
                    );
                }
            }
        }
    }
}

