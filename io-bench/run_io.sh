#!/bin/bash
# run_io.sh <workdir> <out.csv> <log.txt> — IO phases only, for a quiet-disk window.
set -u
WORK=${1:-/data/ubench-tmp}
OUT=${2:-$WORK/results-io.csv}
LOG=${3:-$WORK/log-io.txt}
SRC=$(cd "$(dirname "$0")" && pwd)
mkdir -p "$WORK"
: > "$OUT"; : > "$LOG"
say() { echo "[$(date +%H:%M:%S)] $*" >> "$LOG"; }
echo "# io-only host=$(hostname) date=$(date -Is) load=$(cat /proc/loadavg)" >> "$OUT"
echo "# fs=$(df -T "$WORK" | tail -1)" >> "$OUT"
say "compiling iobench"
gcc -O2 -std=gnu11 -o "$WORK/iobench" "$SRC/iobench.c" -lm 2>>"$LOG" || { say "compile failed"; exit 1; }
for m in clone clone_frag clone_chain fsync fallocate; do
  say "start $m"
  "$WORK/iobench" "$m" --dir "$WORK" >> "$OUT" 2>>"$LOG"
  say "end $m rc=$?"
done
for m in pcache odirect; do
  say "start $m"
  "$WORK/iobench" "$m" --dir "$WORK" >> "$OUT" 2>>"$LOG"
  say "end $m rc=$?"
done
if command -v fio >/dev/null 2>&1; then
  FIO=$WORK/fio.dat
  run_fio() {
    local name="$1"; shift
    say "start fio $name"
    fio --name="$name" --filename="$FIO" --output-format=json --output="$WORK/fio-$name.json" \
        --time_based --runtime=8 --group_reporting "$@" >>"$LOG" 2>&1
    python3 - "$WORK/fio-$name.json" "$name" >>"$OUT" 2>>"$LOG" <<'PY'
import json,sys
d=json.load(open(sys.argv[1])); name=sys.argv[2]
for j in d.get('jobs',[]):
    for op in ('read','write'):
        x=j.get(op,{})
        if x.get('iops',0) > 0:
            print(f"RESULT,io,fio_{name}_{op}_iops,0,1,{x['iops']:.2f},IOPS")
            print(f"RESULT,io,fio_{name}_{op}_bw,0,1,{x['bw']/1024.0:.2f},MiB/s")
            lat=x.get('lat_ns',{}).get('mean')
            if lat: print(f"RESULT,io,fio_{name}_{op}_lat,0,1,{lat:.1f},ns")
PY
    say "end fio $name"
  }
  rm -f "$FIO"
  run_fio seqwrite_1M_qd32 --rw=write --bs=1M --iodepth=32 --ioengine=libaio --direct=1 --size=4G
  run_fio seqread_1M_qd32 --rw=read --bs=1M --iodepth=32 --ioengine=libaio --direct=1 --size=4G
  run_fio randread_4K_qd1 --rw=randread --bs=4K --iodepth=1 --ioengine=libaio --direct=1 --size=4G
  run_fio randread_4K_qd32 --rw=randread --bs=4K --iodepth=32 --ioengine=libaio --direct=1 --size=4G
  run_fio randwrite_4K_qd1_sync --rw=randwrite --bs=4K --iodepth=1 --ioengine=libaio --direct=1 --fsync=1 --size=4G
  rm -f "$FIO"
fi
echo "# load_after=$(cat /proc/loadavg)" >> "$OUT"
echo "IO DONE"
