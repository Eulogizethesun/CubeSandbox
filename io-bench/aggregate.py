#!/usr/bin/env python3
"""Aggregate RESULT lines from the two machines and build comparison tables.

Usage:
  python3 aggregate.py --m950 r1.csv r2.csv --m9654 r1.csv r2.csv --out DIR
"""
import argparse
import csv
import os
import statistics
from collections import defaultdict

LOWER_BETTER_TESTS = {"lat_mem", "lat_mlp", "syscall", "ctx", "c2c", "fault",
                      "timer", "lat_mlp"}
LOWER_BETTER_VARIANTS = ("ficlone", "fsync", "fdatasync", "fallocate", "odirect_rand",
                         "pcache_rand", "rtt")
HIGHER_BETTER_TESTS = {"bw", "memfunc", "vec", "atomic", "crypto", "compress", "perf"}


def load(paths):
    per_round = []
    units = {}
    for p in paths:
        d = defaultdict(list)
        with open(p, errors="ignore") as f:
            for line in f:
                if not line.startswith("RESULT,"):
                    continue
                parts = line.strip().split(",")
                if len(parts) != 7:
                    continue
                _, test, variant, size, threads, value, unit = parts
                try:
                    key = (test, variant, int(size), int(threads))
                    d[key].append(float(value))
                    units[key] = unit
                except ValueError:
                    continue
        per_round.append(d)
    keys = set()
    for d in per_round:
        keys |= set(d.keys())
    med = {}
    spread = {}
    for k in keys:
        vals_per_round = [statistics.median(d[k]) for d in per_round if d.get(k)]
        med[k] = statistics.median(vals_per_round)
        spread[k] = (max(vals_per_round) - min(vals_per_round)) / med[k] * 100 if len(vals_per_round) > 1 and med[k] else 0.0
    return med, spread, units


def advantage(test, variant, unit, m950, m9654):
    """Return 950/9654 advantage; >1 means 950 is better."""
    if m950 is None or m9654 is None or m950 == 0 or m9654 == 0:
        return None
    u = unit.strip()
    if u in ("ns", "us", "ms", "MPKI"):
        lower = True
    elif u in ("GB/s", "MB/s", "MiB/s", "IOPS", "GFLOP/s", "GOP/s", "Mops/s", "IPC"):
        lower = False
    elif u == "MHz":
        return None  # neutral, not a performance metric
    else:
        lower = False
    return (m9654 / m950) if lower else (m950 / m9654)


def fmt(v):
    if v is None:
        return "-"
    if abs(v) >= 1000:
        return f"{v:.0f}"
    if abs(v) >= 10:
        return f"{v:.2f}"
    return f"{v:.3f}"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--m950", nargs="+", required=True)
    ap.add_argument("--m9654", nargs="+", required=True)
    ap.add_argument("--out", default=".")
    a = ap.parse_args()
    os.makedirs(a.out, exist_ok=True)
    m950, s950, u950 = load(a.m950)
    m9654, s9654, u9654 = load(a.m9654)
    keys = sorted(set(m950) | set(m9654), key=lambda k: (k[0], k[1], k[2], k[3]))

    rows = []
    for k in keys:
        v950 = m950.get(k)
        v9654 = m9654.get(k)
        unit = u950.get(k) or u9654.get(k, "")
        adv = advantage(k[0], k[1], unit, v950, v9654)
        rows.append({
            "test": k[0], "variant": k[1], "size": k[2], "threads": k[3],
            "unit": unit,
            "m950": v950, "m9654": v9654, "adv_950": adv,
            "spread950_pct": s950.get(k, 0), "spread9654_pct": s9654.get(k, 0),
        })

    csv_path = os.path.join(a.out, "compare.csv")
    with open(csv_path, "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["test", "variant", "size", "threads", "unit", "m950", "m9654",
                    "adv_950_over_9654", "spread950_pct", "spread9654_pct"])
        for r in rows:
            w.writerow([r["test"], r["variant"], r["size"], r["threads"], r["unit"],
                        r["m950"], r["m9654"], r["adv_950"],
                        round(r["spread950_pct"], 2), round(r["spread9654_pct"], 2)])

    md_path = os.path.join(a.out, "compare.md")
    with open(md_path, "w") as f:
        f.write("| test | variant | size | threads | unit | 950 | 9654 | 950/9654 advantage | spread950% | spread9654% |\n")
        f.write("|---|---|---|---|---|---|---|---|---|---|\n")
        for r in rows:
            f.write(f"| {r['test']} | {r['variant']} | {r['size']} | {r['threads']} | {r['unit']} | "
                    f"{fmt(r['m950'])} | {fmt(r['m9654'])} | {fmt(r['adv_950'])} | "
                    f"{r['spread950_pct']:.1f} | {r['spread9654_pct']:.1f} |\n")

    # stability report
    stab = os.path.join(a.out, "stability.md")
    with open(stab, "w") as f:
        f.write("| test | variant | threads | 950 spread% | 9654 spread% |\n|---|---|---|---|---|\n")
        for r in rows:
            if r["spread950_pct"] > 8 or r["spread9654_pct"] > 8:
                f.write(f"| {r['test']} | {r['variant']} | {r['threads']} | "
                        f"{r['spread950_pct']:.1f} | {r['spread9654_pct']:.1f} |\n")
    print(f"rows={len(rows)} -> {csv_path}, {md_path}, {stab}")


if __name__ == "__main__":
    main()
