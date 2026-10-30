#!/bin/bash
# fsctl.sh <workdir> — FICLONE control experiment on an identical loopback XFS.
# Both machines use the same agcount/reflink/rmapbt geometry, so any remaining
# difference is kernel+CPU+FS-code, not the deployed filesystem layout.
set -u
WORK=$1
MNT=/mnt/ubenchfs
IMG=$WORK/fsctl.img
mkdir -p "$MNT" "$WORK"
cleanup() { umount "$MNT" 2>/dev/null; rm -f "$IMG"; rmdir "$MNT" 2>/dev/null; }
trap cleanup EXIT INT TERM
rm -f "$IMG"
dd if=/dev/zero of="$IMG" bs=1M count=8192 status=none || exit 1
mkfs.xfs -f -q -d agcount=16,size=8g -m reflink=1,rmapbt=1 "$IMG" || exit 2
mount -o loop "$IMG" "$MNT" || exit 3
echo "# loopback xfs control: $(xfs_info "$MNT" | tr '\n' ' ')"
"${IOBENCH:-/tmp/iobench}" clone --dir "$MNT"
"${IOBENCH:-/tmp/iobench}" clone_frag --dir "$MNT"
