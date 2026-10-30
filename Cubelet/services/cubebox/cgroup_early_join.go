// Copyright (c) 2024 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

package cubebox

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/containerd/cgroups/v3"
	"github.com/tencentcloud/CubeSandbox/Cubelet/pkg/log"
)

const hostCgroupRoot = "/sys/fs/cgroup"

// hostCgroupHasEffectiveCpuset reports whether the sandbox cgroup can impose a
// NUMA/CPU placement that must be in effect before the VMM first touches guest
// memory. cgroup v1 keeps cpuset in a separate hierarchy; cgroup v2 folds it
// into the unified tree, so the check depends on the mounted mode.
func hostCgroupHasEffectiveCpuset(cgroupPath string) bool {
	rel := strings.Trim(strings.TrimSpace(cgroupPath), "/")
	if rel == "" {
		// A malformed path cannot be classified safely; keep the early join.
		return true
	}

	mode := cgroups.Mode()
	if mode == cgroups.Unified {
		return unifiedCgroupHasCpuset(rel)
	}
	if cgroupPathExists(filepath.Join(hostCgroupRoot, "cpuset", rel)) {
		return true
	}
	if mode == cgroups.Hybrid {
		return unifiedCgroupHasCpuset(rel)
	}
	return false
}

func unifiedCgroupHasCpuset(rel string) bool {
	dir := filepath.Join(hostCgroupRoot, rel)
	for _, file := range []string{"cpuset.cpus", "cpuset.mems"} {
		if cgroupPathExists(filepath.Join(dir, file)) {
			return true
		}
	}
	return false
}

func cgroupPathExists(path string) bool {
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	// Permission/IO errors are not evidence that cpuset is unused; preserve
	// the existing early-join behavior instead of guessing.
	return true
}

// shouldEarlyJoinHostCgroup decides whether the pod annotation carrying the
// sandbox cgroup path should be injected. If the cgroup has no effective
// cpuset, the pre-VMM join has no placement semantics that the late
// setCgroup cannot replace; omit the annotation and let the shim skip it.
func shouldEarlyJoinHostCgroup(ctx context.Context, cgroupPath string) bool {
	if hostCgroupHasEffectiveCpuset(cgroupPath) {
		return true
	}
	log.G(ctx).Infof("host cgroup early join skipped: cgroup=%s cpuset=absent", cgroupPath)
	return false
}
