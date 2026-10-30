// Copyright (c) 2024 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

package cubebox

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tencentcloud/CubeSandbox/Cubelet/pkg/constants"
	"github.com/tencentcloud/CubeSandbox/Cubelet/plugins/cube/internals/cgroup"
	"github.com/tencentcloud/CubeSandbox/Cubelet/plugins/workflow"
)

func TestSandboxCgroupPathAnnotationFor(t *testing.T) {
	t.Run("numa bound sandbox carries the v2 numa group path", func(t *testing.T) {
		cgInfo := &cgroup.Info{
			CgroupID:  "/cube_sandbox_v2/sandbox/numa0/5",
			UsePoolV2: true,
		}
		anno := sandboxCgroupPathAnnotationFor(cgInfo, true)
		require.NotNil(t, anno)
		assert.Equal(t, "/cube_sandbox_v2/sandbox/numa0/5", anno[constants.AnnotationsCgroupPath])
	})

	t.Run("v1 pool path is carried when early join is needed", func(t *testing.T) {
		cgInfo := &cgroup.Info{CgroupID: "/cube_sandbox/sandbox/7"}
		anno := sandboxCgroupPathAnnotationFor(cgInfo, true)
		require.NotNil(t, anno)
		assert.Equal(t, "/cube_sandbox/sandbox/7", anno[constants.AnnotationsCgroupPath])
	})

	t.Run("policy can suppress the annotation", func(t *testing.T) {
		cgInfo := &cgroup.Info{CgroupID: "/cube_sandbox/sandbox/7"}
		assert.Nil(t, sandboxCgroupPathAnnotationFor(cgInfo, false))
	})

	t.Run("absent or invalid CgroupInfo yields no annotation", func(t *testing.T) {
		assert.Nil(t, sandboxCgroupPathAnnotationFor(nil, true))
		assert.Nil(t, sandboxCgroupPathAnnotation(context.Background(), &workflow.CreateContext{}))
		assert.Nil(t, sandboxCgroupPathAnnotation(context.Background(), &workflow.CreateContext{
			CgroupInfo: "not-a-cgroup-info",
		}))
	})
}
