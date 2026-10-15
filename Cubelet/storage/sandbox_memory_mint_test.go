// Copyright (c) 2026 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tencentcloud/CubeSandbox/Cubelet/pkg/cubecow"
	"github.com/tencentcloud/CubeSandbox/Cubelet/storage/cow"
)

// seedSandboxMemoryMint publishes a completed mint entry the way
// runSandboxMemoryMint would, without touching the store.
func seedSandboxMemoryMint(t *testing.T, sid string, err error) *sandboxMemoryMint {
	t.Helper()
	entry := &sandboxMemoryMint{
		sourceVol: "tpl-snap-8-memory",
		ownVol:    SandboxMemoryName(sid),
		originVol: "tpl-tpl-a-memory",
		done:      make(chan struct{}),
		err:       err,
	}
	sandboxMemoryMints.Lock()
	sandboxMemoryMints.m[sid] = entry
	sandboxMemoryMints.Unlock()
	t.Cleanup(func() {
		sandboxMemoryMints.Lock()
		delete(sandboxMemoryMints.m, sid)
		sandboxMemoryMints.Unlock()
	})
	close(entry.done)
	return entry
}

func TestPromoteSandboxMemoryOwnForPauseRenamesOwn(t *testing.T) {
	sid := "sb-mint-1"
	own := SandboxMemoryName(sid)
	engine := &fakeCowEngine{volumeInfos: map[string]*cubecow.Volume{
		own: {DevicePath: "/x/volumes/" + own + "/" + own, SizeBytes: 1 << 20},
	}}
	useTestCowStorage(t, engine)
	seedSandboxMemoryMint(t, sid, nil)

	obj, detail, ok := PromoteSandboxMemoryOwnForPause(context.Background(), cow.BackendXFS, sid, "snap-9")
	require.True(t, ok, detail)
	require.NotNil(t, obj)
	assert.Equal(t, "tpl-snap-9-memory", obj.Name)
	assert.Equal(t, cowKindSnapshot, obj.Kind)
	assert.Equal(t, [][3]string{{own, "tpl-snap-9-memory", "tpl-tpl-a-memory"}}, engine.promotedVolumes)

	// The registry entry is consumed by the pause that promoted it.
	sandboxMemoryMints.Lock()
	_, still := sandboxMemoryMints.m[sid]
	sandboxMemoryMints.Unlock()
	assert.False(t, still)
}

func TestPromoteSandboxMemoryOwnForPauseFallsBackOnCloneFailure(t *testing.T) {
	sid := "sb-mint-2"
	engine := &fakeCowEngine{}
	useTestCowStorage(t, engine)
	seedSandboxMemoryMint(t, sid, errors.New("clone boom"))

	obj, detail, ok := PromoteSandboxMemoryOwnForPause(context.Background(), cow.BackendXFS, sid, "snap-9")
	require.False(t, ok)
	require.Nil(t, obj)
	assert.Contains(t, detail, "clone boom")
	assert.Empty(t, engine.promotedVolumes)
}

func TestRunSandboxMemoryMintDeletesOwnOnCloneFailure(t *testing.T) {
	sid := "sb-mint-3"
	engine := &fakeCowEngine{createVolumeFromSnapshotErr: errors.New("ficlone failed")}
	useTestCowStorage(t, engine)

	entry := &sandboxMemoryMint{
		sourceVol: "tpl-snap-8-memory",
		ownVol:    SandboxMemoryName(sid),
		originVol: "tpl-tpl-a-memory",
		done:      make(chan struct{}),
	}
	sandboxMemoryMints.Lock()
	sandboxMemoryMints.m[sid] = entry
	sandboxMemoryMints.Unlock()
	t.Cleanup(func() {
		sandboxMemoryMints.Lock()
		delete(sandboxMemoryMints.m, sid)
		sandboxMemoryMints.Unlock()
	})

	runSandboxMemoryMint(context.Background(), cow.BackendXFS, sid, entry)
	<-entry.done
	require.Error(t, entry.err)
	assert.Contains(t, entry.err.Error(), "ficlone failed")
	// The failed clone target must not survive as a ready-looking own volume.
	assert.Contains(t, engine.deletedVolumes, entry.ownVol)
}

func TestInvalidateSandboxMemoryMintRemovesReadyOwn(t *testing.T) {
	sid := "sb-mint-4"
	engine := &fakeCowEngine{}
	useTestCowStorage(t, engine)
	seedSandboxMemoryMint(t, sid, nil)

	InvalidateSandboxMemoryMint(context.Background(), cow.BackendXFS, sid)
	assert.Contains(t, engine.deletedVolumes, SandboxMemoryName(sid))

	obj, _, ok := PromoteSandboxMemoryOwnForPause(context.Background(), cow.BackendXFS, sid, "snap-9")
	require.False(t, ok)
	require.Nil(t, obj)
}
