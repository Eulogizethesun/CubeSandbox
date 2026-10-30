#!/usr/bin/env python3
"""Summarize the resume-stage instrumentation for one sandbox id.

Reads on the node (override with CUBE_LOG_ROOT):
  $CUBE_LOG_ROOT/Cubelet/Cubelet-req.log       CUBE_PERF_TRACE batches
  $CUBE_LOG_ROOT/CubeShim/cube-shim-stat.log   PerfTrace markers/spans
  $CUBE_LOG_ROOT/CubeVmm/vmm.log               cubeprof resume phases
  $CUBE_LOG_ROOT/CubeMaster/cubemaster-req.log cubeprof master resume phases
"""
import json
import os
import re
import sys

SID = sys.argv[1] if len(sys.argv) > 1 else ""
ROOT = os.environ.get("CUBE_LOG_ROOT", "/data/log")
CUBELET = os.path.join(ROOT, "Cubelet/Cubelet-req.log")
SHIM = os.path.join(ROOT, "CubeShim/cube-shim-stat.log")
VMM = os.path.join(ROOT, "CubeVmm/vmm.log")
MASTER = os.path.join(ROOT, "CubeMaster/cubemaster-req.log")


def read(path):
    try:
        with open(path, "r", errors="replace") as f:
            return f.readlines()
    except OSError as e:
        print("open %s: %s" % (path, e), file=sys.stderr)
        return []


def cubelet_batches():
    out = []
    for line in read(CUBELET):
        if "CUBE_PERF_TRACE" not in line or SID not in line:
            continue
        try:
            outer = json.loads(line)
        except json.JSONDecodeError:
            continue
        content = outer.get("LogContent", "")
        m = re.search(r"CUBE_PERF_TRACE (\{.*\})\s*$", content)
        if not m:
            continue
        try:
            batch = json.loads(m.group(1))
        except json.JSONDecodeError:
            continue
        if batch.get("sandbox_id") != SID:
            continue
        spans = batch.get("spans") or []
        kinds = {s.get("kind") for s in spans}
        stages = {s.get("stage") for s in spans}
        if not any(str(x).startswith("Resume.") for x in stages):
            continue
        out.append(batch)
    return out


def _cubelet_batch():
    batches = cubelet_batches()
    if not batches:
        return None
    return batches[-1]


def _summarize_cubelet(batch):
    if not batch:
        return {}
    marks = {s["stage"]: s["duration_us"] for s in batch["spans"] if s["kind"] == "cumulative"}
    req = next((s["duration_us"] for s in batch["spans"] if s["stage"] == "cubelet.request"), 0.0)
    out = {"request_total": req}
    for key, a, b in [
        ("front", None, "Engine.CallStart"),
        ("engine_enter_gap", "Engine.CallStart", "Engine.Enter"),
        ("engine_pre", "Engine.Tombstone", "Workflow.Start"),
        ("workflow_pre", "Workflow.Start", "Container.NewContainer"),
        ("newtask", "Container.NewContainer", "Container.NewTask"),
        ("task_wait_start", "Container.NewTask", "Container.TaskStart"),
        ("probe", "Probe.ContainerDone", "Probe.HealthDone"),
        ("post", "Probe.PostDone", "Response.Ready"),
    ]:
        if a is None:
            out[key] = marks.get(b, 0.0)
        else:
            out[key] = marks.get(b, 0.0) - marks.get(a, 0.0)
    return out


def _matching_shim_op(rows):
    """Pick the TaskCreate operation whose shim process started for our resume.

    The clock id suffix is the shim process start in unix ns; the Cubelet
    request origin is the same clock, so the resume shim starts just after it.
    """
    batch = _cubelet_batch()
    origin = batch["origin_unix_ns"] if batch else None
    ops = {}
    for action, callee, ca, cost, pt in rows:
        if pt["Kind"] != "cumulative" or not ca.startswith("TaskCreate."):
            continue
        ops.setdefault(pt["ClockId"], []).append((pt["StartMonoNs"], pt["EndMonoNs"], ca))
    best = None
    for clock, items in ops.items():
        try:
            suffix = int(clock.split(":", 1)[1])
        except (IndexError, ValueError):
            continue
        if origin is not None and suffix < origin - 20_000_000:
            continue
        dist = abs(suffix - origin) if origin is not None else suffix
        if best is None or dist < best[0]:
            best = (dist, clock, sorted(items))
    return best[2] if best else []


def _summarize_shim(items):
    if not items:
        return {}
    at = {}
    for _, end, ca in items:
        at[ca[len("TaskCreate."):]] = end / 1000.0
    out = {"taskcreate_total": max(at.values()) if at else 0.0}
    for key, a, b in [
        ("cgroup", "InitSandbox", "CreateSandbox.Cgroup"),
        ("startvm", "CreateSandbox.Cgroup", "CreateSandbox.StartVm"),
        ("launch_vmm", "StartVm.Enter", "StartVm.LaunchVmm"),
        ("vmm", "Restore.VmmLock", "Restore.VmmDone"),
        ("agent_connect", "AgentConnect.Enter", "CreateSandbox.AgentConnect"),
        ("handshake", "AgentConnect.Enter", "AgentConnect.Handshake"),
        ("reset", "AgentConnect.Handshake", "AgentConnect.Reset"),
        ("agent_create_sandbox", "CreateSandbox.Storages", "CreateSandbox.AgentCreateSandbox"),
        ("container_rpc", "CreateContainer.New", "CreateContainer.AgentRpc"),
        ("shim_total", None, "CreatePodSandbox"),
    ]:
        if a is None:
            out[key] = at.get(b, 0.0)
        else:
            out[key] = at.get(b, 0.0) - at.get(a, 0.0)
    return out


def _summarize_vmm():
    data = {}
    for line in read(VMM):
        if SID not in line:
            continue
        m = re.search(r"cubeprof resume phase=(\S+) us=(\d+)", line)
        if m:
            data[m.group(1)] = int(m.group(2))
    return data


def _summarize_master():
    """Last resume request's phase deltas."""
    reqs = []
    for line in read(MASTER):
        if "cubeprof master resume" not in line:
            continue
        try:
            d = json.loads(line)
        except json.JSONDecodeError:
            continue
        content = d.get("LogContent", "")
        if SID not in content:
            continue
        m = re.search(r"phase=(\S+) us=(\d+) total_us=(\d+)", content)
        if m:
            reqs.append((d.get("RequestId"), m.group(1), int(m.group(2)), int(m.group(3))))
    if not reqs:
        return {}
    last_rid = reqs[-1][0]
    return {p: us for r, p, us, _ in reqs if r == last_rid}


def summary():
    print("== SUMMARY %s ==" % SID)
    cl = _summarize_cubelet(_cubelet_batch())
    sh = _summarize_shim(_matching_shim_op(
        [(d.get("Action"), d.get("Callee"), d.get("CalleeAction"), d.get("CostTime"), d["PerfTrace"])
         for d in (json.loads(l) for l in read(SHIM) if SID in l and "PerfTrace" in l)
         if d.get("InstanceId") == SID and d.get("PerfTrace")]))
    vm = _summarize_vmm()
    ma = _summarize_master()
    for k, v in cl.items():
        print("  cubelet.%-24s %9.1f us" % (k, v))
    for k, v in sh.items():
        print("  shim.%-27s %9.1f us" % (k, v))
    for k in ("total", "new_from_snapshot", "vm_restore", "vm_resume", "mem_new"):
        if k in vm:
            print("  vmm.%-28s %9d us" % (k, vm[k]))
    for k, v in ma.items():
        print("  master.%-26s %9d us" % (k, v))
    if cl and sh:
        print("  crosscheck: cubelet.newtask - shim.taskcreate_total = %.1f us" %
              (cl["newtask"] - sh["taskcreate_total"]))
    print()


def print_cubelet():
    batches = cubelet_batches()
    print("\n== Cubelet perftrace (%d resume batches) ==" % len(batches))
    for b in batches:
        spans = b["spans"]
        req = next((s for s in spans if s["stage"] == "cubelet.request"), None)
        print("request_id=%s origin_unix_ns=%s total_us=%.1f" %
              (b["request_id"], b["origin_unix_ns"], req["duration_us"] if req else -1))
        cum = sorted((s for s in spans if s["kind"] == "cumulative"), key=lambda s: s["duration_us"])
        prev = 0.0
        print("  cumulative checkpoints (delta from previous):")
        for s in cum:
            d = s["duration_us"]
            print("    %-28s at=%9.1f  delta=%8.1f" % (s["stage"], d, d - prev))
            prev = d
        spans_only = sorted((s for s in spans if s["kind"] == "span"), key=lambda s: -s["duration_us"])
        if spans_only:
            print("  spans:")
            for s in spans_only[:15]:
                print("    %-28s dur=%8.1f start=%9.1f" % (s["stage"], s["duration_us"], s["start_offset_us"]))


def print_shim():
    rows = []
    for line in read(SHIM):
        if SID not in line or "PerfTrace" not in line:
            continue
        try:
            d = json.loads(line)
        except json.JSONDecodeError:
            continue
        if d.get("InstanceId") != SID:
            continue
        pt = d.get("PerfTrace")
        if not pt:
            continue
        rows.append((d.get("Action"), d.get("Callee"), d.get("CalleeAction"), d.get("CostTime"), pt))
    print("\n== Shim PerfTrace entries: %d ==" % len(rows))
    # group by (clock, operation id): the operation counter restarts in every
    # fresh shim process, so OperationId alone collides across cycles.
    by_op = {}
    for action, callee, callee_action, cost, pt in rows:
        by_op.setdefault((pt["ClockId"], pt["OperationId"]), []).append(
            (pt["StartMonoNs"], pt["EndMonoNs"], pt["DurationNs"], action, callee,
             callee_action, pt["Kind"]))
    for key in sorted(by_op, key=lambda k: min(x[0] for x in by_op[k])):
        items = sorted(by_op[key])
        kind = items[0][6]
        if kind != "cumulative":
            start, end, dur, action, callee, ca, _ = items[0]
            if end - start > 500_000 or key[1] in (1, 9, 10):
                print("  clock=%s op=%-3d span %-10s %-9s %-30s dur_us=%9.1f" %
                      (key[0], key[1], action, callee, ca, (end - start) / 1000.0))
            continue
        prev_end = items[0][0]
        base = items[0][0]
        total = max(x[1] for x in items)
        print("  clock=%s op=%-3d %s/%s total_us=%.1f:" %
              (key[0], key[1], items[0][3], items[0][4], (total - base) / 1000.0))
        for start, end, dur, action, callee, ca, _ in items:
            stage = ca
            if stage.startswith("TaskCreate."):
                stage = stage[len("TaskCreate."):]
            print("    %-40s at_us=%9.1f delta_us=%8.1f" %
                  (stage, (end - base) / 1000.0, (end - prev_end) / 1000.0))
            prev_end = end


def print_vmm():
    print("\n== VMM cubeprof resume phases ==")
    for line in read(VMM):
        if SID not in line or "cubeprof resume" not in line:
            continue
        m = re.search(r"cubeprof resume phase=(\S+) us=(\d+)", line)
        if m:
            print("  %-24s us=%s" % (m.group(1), m.group(2)))
    # memory phases
    print("== VMM cubeprof resume memory phases ==")
    for line in read(VMM):
        if SID not in line:
            continue
        m = re.search(r"cubeprof resume phase=(mem_\S+) us=(\d+)(.*)", line)
        if m:
            print("  %-24s us=%s%s" % (m.group(1), m.group(2), m.group(3).rstrip()))


def print_master():
    print("\n== Master cubeprof resume phases (last 3 requests) ==")
    reqs = []
    for line in read(MASTER):
        if "cubeprof master resume" not in line:
            continue
        try:
            d = json.loads(line)
        except json.JSONDecodeError:
            continue
        if d.get("InstanceId") not in ("", None) and d.get("InstanceId") != SID:
            continue
        content = d.get("LogContent", "")
        if SID not in content:
            continue
        m = re.search(r"phase=(\S+) us=(\d+) total_us=(\d+)", content)
        if not m:
            continue
        reqs.append((d.get("RequestId"), m.group(1), int(m.group(2)), int(m.group(3))))
    # keep the last 3 request ids by order of appearance
    order = []
    for rid, *_ in reqs:
        if rid not in order:
            order.append(rid)
    for rid in order[-3:]:
        print("  request_id=%s" % rid)
        for r, phase, us, total in reqs:
            if r == rid:
                print("    %-22s us=%8d total_us=%8d" % (phase, us, total))


if __name__ == "__main__":
    if not SID:
        print("usage: resume_trace_analyze.py <sandbox-id>", file=sys.stderr)
        sys.exit(2)
    summary()
    print_cubelet()
    print_shim()
    print_vmm()
    print_master()
