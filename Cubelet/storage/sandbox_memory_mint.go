// Copyright (c) 2026 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

package storage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tencentcloud/CubeSandbox/Cubelet/pkg/constants"
	"github.com/tencentcloud/CubeSandbox/Cubelet/pkg/log"
	"github.com/tencentcloud/CubeSandbox/Cubelet/pkg/utils"
)

// sandboxMemoryMint is the in-process state of the background clone that
// gives a resumed XFS sandbox its private memory volume (sb-<id>-memory).
// The next pause promotes that volume into its snapshot by rename, so the
// reflink clone never lands on the pause critical path. A Cubelet restart
// simply empties the registry; pause then clones the restore image as
// before, which is always correct.
type sandboxMemoryMint struct {
	sourceVol string
	ownVol    string
	originVol string
	done      chan struct{}
	err       error
}

var sandboxMemoryMints = struct {
	sync.Mutex
	m map[string]*sandboxMemoryMint
}{m: make(map[string]*sandboxMemoryMint)}

// mintTimeout bounds one background clone; the deepest measured chain takes
// well under a second, so this only trips on a broken filesystem.
const mintTimeout = 10 * time.Minute

// MintSandboxMemoryAfterResume starts the background clone for a same-node
// XFS pause resume. Call it only after Create returned success; non-XFS
// backends and cross-node restores keep their existing own-volume flow.
func MintSandboxMemoryAfterResume(ctx context.Context, backend, sandboxID string, annotations map[string]string) {
	if !isXFSCleanupBackend(backend) || strings.TrimSpace(sandboxID) == "" {
		return
	}
	if CrossNodeSandboxImport(annotations) != nil {
		return
	}
	sourceVol, sourceKind, err := resolveMintSourceVolume(ctx, backend, annotations)
	if err != nil {
		log.G(ctx).Warnf("sandbox memory mint: sandbox %s resolve source: %v", sandboxID, err)
		return
	}
	if sourceVol == "" {
		return
	}
	devPath, err := ResolveObjectPathFor(ctx, backend, sourceVol, sourceKind)
	if err != nil {
		log.G(ctx).Warnf("sandbox memory mint: sandbox %s resolve path of %s: %v", sandboxID, sourceVol, err)
		return
	}
	originVol := filepath.Base(filepath.Dir(devPath))
	if originVol == "" || originVol == "." || originVol == string(filepath.Separator) {
		log.G(ctx).Warnf("sandbox memory mint: sandbox %s derived empty origin from %s", sandboxID, devPath)
		return
	}

	entry := &sandboxMemoryMint{
		sourceVol: sourceVol,
		ownVol:    SandboxMemoryName(sandboxID),
		originVol: originVol,
		done:      make(chan struct{}),
	}
	sandboxMemoryMints.Lock()
	sandboxMemoryMints.m[sandboxID] = entry
	sandboxMemoryMints.Unlock()

	go runSandboxMemoryMint(ctx, backend, sandboxID, entry)
}

// runSandboxMemoryMint clones the restore image onto the sandbox's own
// volume and publishes the entry for the next pause. An entry superseded by
// a re-mint or an invalidate discards whatever it made.
func runSandboxMemoryMint(ctx context.Context, backend, sandboxID string, entry *sandboxMemoryMint) {
	mintCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), mintTimeout)
	defer cancel()

	err := cloneSandboxOwnVolume(mintCtx, backend, sandboxID, entry)
	if err == nil {
		err = recordSandboxOwnMemory(mintCtx, sandboxID, entry.ownVol)
	}

	sandboxMemoryMints.Lock()
	current := sandboxMemoryMints.m[sandboxID] == entry
	if current {
		entry.err = err
	}
	sandboxMemoryMints.Unlock()

	switch {
	case !current:
		bestEffortDeleteOwnMemory(context.WithoutCancel(ctx), backend, entry.ownVol)
	case err != nil:
		log.G(ctx).Warnf("sandbox memory mint: sandbox %s clone %s -> %s failed: %v",
			sandboxID, entry.sourceVol, entry.ownVol, err)
		bestEffortDeleteOwnMemory(context.WithoutCancel(ctx), backend, entry.ownVol)
	default:
		log.G(ctx).Infof("sandbox memory mint: sandbox %s own volume %s ready (source %s)",
			sandboxID, entry.ownVol, entry.sourceVol)
	}
	close(entry.done)
}

// cloneSandboxOwnVolume drops any leftover own volume and clones this
// resume's restore image onto it. The delete is strict: resolving a stale
// clone would silently hand pause a base that no longer matches the VM.
func cloneSandboxOwnVolume(ctx context.Context, backend, sandboxID string, entry *sandboxMemoryMint) error {
	store, err := requireCowStoreFor(backend)
	if err != nil {
		return err
	}
	_ = store.DeactivateByKind(ctx, entry.ownVol, cowKindVolume)
	if err := store.DeleteByKind(ctx, entry.ownVol, cowKindVolume); err != nil {
		return fmt.Errorf("drop stale own volume %s: %w", entry.ownVol, err)
	}
	start := time.Now()
	cloned, err := store.CloneSandboxMemory(ctx, sandboxID, entry.sourceVol)
	if err != nil {
		return fmt.Errorf("clone %s -> %s: %w", entry.sourceVol, entry.ownVol, err)
	}
	if cloned == nil || strings.TrimSpace(cloned.FilePath) == "" {
		return fmt.Errorf("clone %s -> %s: empty device path", entry.sourceVol, entry.ownVol)
	}
	log.G(ctx).Infof("sandbox memory mint: cloned %s -> %s in %dms",
		entry.sourceVol, entry.ownVol, time.Since(start).Milliseconds())
	return nil
}

// PromoteSandboxMemoryOwnForPause consumes the sandbox's pre-cloned own
// memory volume as this pause's memory artifact: the volume's file is
// renamed to tpl-<snapshotID>-memory under the origin directory the restore
// image lives in. ok=false keeps the caller on the legacy clone ladder;
// detail is empty when there was nothing to promote.
func PromoteSandboxMemoryOwnForPause(ctx context.Context, backend, sandboxID, snapshotID string) (*CowSnapshotObject, string, bool) {
	sandboxMemoryMints.Lock()
	entry := sandboxMemoryMints.m[sandboxID]
	sandboxMemoryMints.Unlock()
	if entry == nil {
		return nil, "", false
	}
	select {
	case <-entry.done:
	case <-ctx.Done():
		return nil, "waiting for the background clone: " + ctx.Err().Error(), false
	}
	sandboxMemoryMints.Lock()
	if sandboxMemoryMints.m[sandboxID] == entry {
		delete(sandboxMemoryMints.m, sandboxID)
	}
	sandboxMemoryMints.Unlock()
	if entry.err != nil {
		return nil, entry.err.Error(), false
	}

	devPath, err := ResolveObjectPathFor(ctx, backend, entry.ownVol, cowKindVolume)
	if err != nil {
		return nil, fmt.Sprintf("resolve own volume %s: %v", entry.ownVol, err), false
	}
	own := &CowSnapshotObject{Name: entry.ownVol, Kind: cowKindVolume, DevPath: devPath}

	start := time.Now()
	memoryObject, err := PromoteMemoryVolumeFor(ctx, backend, own, snapshotID, entry.originVol)
	if err != nil {
		return nil, fmt.Sprintf("promote own volume %s: %v", entry.ownVol, err), false
	}
	// The own volume name is gone; drop the recorded pointer so destroy and
	// rollback cleanups do not chase a renamed object.
	clearSandboxOwnMemoryRecord(ctx, sandboxID, entry.ownVol)
	return memoryObject, fmt.Sprintf("own %s -> %s (origin %s) in %dus",
		entry.ownVol, memoryObject.Name, entry.originVol, time.Since(start).Microseconds()), true
}

// InvalidateSandboxMemoryMint drops the mint state ahead of a lifecycle
// step that invalidates the own volume (rollback, destroy). A clone still
// in flight is discarded by its completion path.
func InvalidateSandboxMemoryMint(ctx context.Context, backend, sandboxID string) {
	if strings.TrimSpace(sandboxID) == "" {
		return
	}
	sandboxMemoryMints.Lock()
	entry := sandboxMemoryMints.m[sandboxID]
	delete(sandboxMemoryMints.m, sandboxID)
	sandboxMemoryMints.Unlock()
	if entry == nil {
		return
	}
	select {
	case <-entry.done:
		if entry.err == nil {
			bestEffortDeleteOwnMemory(ctx, backend, entry.ownVol)
		}
	default:
		// Still running: the completion path sees itself superseded and
		// removes the file.
	}
}

func bestEffortDeleteOwnMemory(ctx context.Context, backend, ownVol string) {
	store, err := requireCowStoreFor(backend)
	if err != nil {
		return
	}
	_ = store.DeactivateByKind(ctx, ownVol, cowKindVolume)
	if err := store.DeleteByKind(ctx, ownVol, cowKindVolume); err != nil {
		log.G(ctx).Warnf("sandbox memory mint: delete own volume %s: %v", ownVol, err)
	}
}

// resolveMintSourceVolume resolves the memory volume this resume booted
// from, mirroring prefetchRestoreMemoryVolURL's catalog lookup. Empty means
// the sandbox is not bound to a memory snapshot; the mint is a no-op.
func resolveMintSourceVolume(ctx context.Context, backend string, annotations map[string]string) (string, string, error) {
	logicalID := strings.TrimSpace(annotations[constants.MasterAnnotationRuntimeSnapshotID])
	if logicalID == "" {
		logicalID = strings.TrimSpace(annotations[constants.MasterAnnotationAppSnapshotTemplateID])
	}
	if logicalID == "" {
		return "", "", nil
	}
	entry, err := GetLocalSnapshotFor(ctx, backend, logicalID)
	if err != nil {
		return "", "", err
	}
	if entry == nil {
		return "", "", nil
	}
	volumeName := strings.TrimSpace(entry.MemoryVol)
	if volumeName == "" {
		return "", "", nil
	}
	kind := strings.TrimSpace(entry.MemoryKind)
	if kind == "" {
		kind = cowKindVolume
	}
	normalized, err := normalizeCowKind(kind)
	if err != nil {
		return "", "", err
	}
	return volumeName, normalized, nil
}

// recordSandboxOwnMemory persists the own volume on the sandbox record so
// destroy and rollback clean it up through the existing release paths.
func recordSandboxOwnMemory(ctx context.Context, sandboxID, ownVol string) error {
	if localStorage == nil || localStorage.db == nil {
		return fmt.Errorf("storage is not initialized")
	}
	info, err := localStorage.readBackendFileInfo(ctx, sandboxID)
	if err != nil {
		if errors.Is(err, utils.ErrorKeyNotFound) || errors.Is(err, utils.ErrorBucketNotFound) {
			return fmt.Errorf("sandbox record %s is gone", sandboxID)
		}
		return err
	}
	if info == nil {
		return fmt.Errorf("sandbox record %s is empty", sandboxID)
	}
	if strings.TrimSpace(info.ImportedMemoryVol) == ownVol {
		return nil
	}
	info.ImportedMemoryVol = ownVol
	info.UpdateAt = time.Now()
	return localStorage.writeBackendFileInfo(ctx, sandboxID, info)
}

// clearSandboxOwnMemoryRecord removes the own volume pointer once pause has
// consumed it; best effort because a stale pointer only warns at destroy.
func clearSandboxOwnMemoryRecord(ctx context.Context, sandboxID, ownVol string) {
	if localStorage == nil || localStorage.db == nil {
		return
	}
	info, err := localStorage.readBackendFileInfo(ctx, sandboxID)
	if err != nil || info == nil {
		return
	}
	if strings.TrimSpace(info.ImportedMemoryVol) != ownVol {
		return
	}
	info.ImportedMemoryVol = ""
	info.UpdateAt = time.Now()
	if err := localStorage.writeBackendFileInfo(ctx, sandboxID, info); err != nil {
		log.G(ctx).Warnf("sandbox memory mint: clear own volume record %s: %v", ownVol, err)
	}
}
