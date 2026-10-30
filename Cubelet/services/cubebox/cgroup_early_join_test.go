// Copyright (c) 2024 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

package cubebox

import "testing"

func TestParseHostCgroupEarlyJoinMode(t *testing.T) {
	tests := []struct {
		raw   string
		want  string
		valid bool
	}{
		{"", hostCgroupEarlyJoinAlways, true},
		{"always", hostCgroupEarlyJoinAlways, true},
		{" ALWAYS ", hostCgroupEarlyJoinAlways, true},
		{"auto", hostCgroupEarlyJoinAuto, true},
		{"Never", hostCgroupEarlyJoinNever, true},
		{"bogus", hostCgroupEarlyJoinAlways, false},
	}
	for _, tt := range tests {
		got, valid := parseHostCgroupEarlyJoinMode(tt.raw)
		if got != tt.want || valid != tt.valid {
			t.Fatalf("parseHostCgroupEarlyJoinMode(%q) = (%q, %v), want (%q, %v)",
				tt.raw, got, valid, tt.want, tt.valid)
		}
	}
}

func TestHostCgroupHasEffectiveCpusetRejectsEmptyPath(t *testing.T) {
	if !hostCgroupHasEffectiveCpuset("") {
		t.Fatal("empty cgroup path must keep the conservative early-join behavior")
	}
}
