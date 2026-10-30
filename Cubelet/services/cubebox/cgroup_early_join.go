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
	"github.com/tencentcloud/CubeSandbox/Cubelet/pkg/config"
	"github.com/tencentcloud/CubeSandbox/Cubelet/pkg/log"
)

const (
	hostCgroupRoot = "/sys/fs/cgroup"

	hostCgroupEarlyJoinAlways = "always"
	hostCgroupEarlyJoinAuto   = "auto"
	hostCgroupEarlyJoinNever  = "never"
)

// parseHostCgroupEarlyJoinMode returns the effective mode and whether the
// configured value was recognized. Empty and unknown values fall back to
// "always" so a malformed config cannot silently change placement semantics.
func parseHostCgroupEarlyJoinMode(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", hostCgroupEarlyJoinAlways:
		return hostCgroupEarlyJoinAlways, true
	case hostCgroupEarlyJoinAuto:
		return hostCgroupEarlyJoinAuto, true
	case hostCgroupEarlyJoinNever:
		return hostCgroupEarlyJoinNever, true
	default:
		return hostCgroupEarlyJoinAlways, false
	}
}

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
// sandbox cgroup path should be injected. The shim already treats a missing
// annotation as "skip early join", and cubelet's late setCgroup remains the
// idempotent mover.
func shouldEarlyJoinHostCgroup(ctx context.Context, cgroupPath string) bool {
	conf := config.GetConfig()
	raw := ""
	if conf != nil && conf.Common != nil {
		raw = conf.Common.HostCgroupEarlyJoin
	}

	mode, valid := parseHostCgroupEarlyJoinMode(raw)
	if !valid {
		log.G(ctx).Warnf("unknown host_cgroup_early_join=%q; falling back to %s", raw, hostCgroupEarlyJoinAlways)
	}

	switch mode {
	case hostCgroupEarlyJoinNever:
		return false
	case hostCgroupEarlyJoinAuto:
		if !hostCgroupHasEffectiveCpuset(cgroupPath) {
			log.G(ctx).Infof("host cgroup early join skipped: cgroup=%s mode=auto cpuset=absent", cgroupPath)
			return false
		}
		return true
	default:
		return true
	}
}
