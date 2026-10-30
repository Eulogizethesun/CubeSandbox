// Copyright (c) 2024 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

package cubebox

import "testing"

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
