// Copyright (c) 2024 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

package cubebox

import (
	"context"
	"testing"

	"github.com/tencentcloud/CubeSandbox/Cubelet/pkg/constants"
	"github.com/tencentcloud/CubeSandbox/Cubelet/plugins/cube/internals/cgroup"
	"github.com/tencentcloud/CubeSandbox/Cubelet/plugins/workflow"
)

func TestSandboxCgroupPathAnnotationFor(t *testing.T) {
	cgInfo := &cgroup.Info{
		CgroupID:  "/cube_sandbox_v2/sandbox/numa0/5",
		UsePoolV2: true,
	}
	anno := sandboxCgroupPathAnnotationFor(cgInfo, true)
	if anno == nil || anno[constants.AnnotationsCgroupPath] != cgInfo.CgroupID {
		t.Fatalf("expected cgroup path annotation %q, got %#v", cgInfo.CgroupID, anno)
	}
	if got := sandboxCgroupPathAnnotationFor(cgInfo, false); got != nil {
		t.Fatalf("expected no annotation when early join is not needed, got %#v", got)
	}
	if got := sandboxCgroupPathAnnotationFor(nil, true); got != nil {
		t.Fatalf("expected no annotation for nil CgroupInfo, got %#v", got)
	}
}

func TestSandboxCgroupPathAnnotationWithoutCgroupInfo(t *testing.T) {
	if got := sandboxCgroupPathAnnotation(context.Background(), &workflow.CreateContext{}); got != nil {
		t.Fatalf("expected no annotation without CgroupInfo, got %#v", got)
	}
}

func TestHostCgroupHasEffectiveCpusetRejectsEmptyPath(t *testing.T) {
	if !hostCgroupHasEffectiveCpuset("") {
		t.Fatal("empty cgroup path must keep the conservative early-join behavior")
	}
}

func TestHostCgroupHasEffectiveCpusetForMissingPath(t *testing.T) {
	if hostCgroupHasEffectiveCpuset("/zz-cube-no-such-cgroup") {
		t.Fatal("a missing cgroup path must not claim an effective cpuset")
	}
}
