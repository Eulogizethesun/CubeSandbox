// Copyright (c) 2024 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

package cubebox

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tencentcloud/CubeSandbox/Cubelet/pkg/constants"
	"github.com/tencentcloud/CubeSandbox/Cubelet/plugins/cube/internals/cgroup"
	"github.com/tencentcloud/CubeSandbox/Cubelet/plugins/workflow"
)

func TestSandboxCgroupPathAnnotation(t *testing.T) {
	t.Run("numa bound sandbox carries the v2 numa group path", func(t *testing.T) {
		opts := &workflow.CreateContext{
			CgroupInfo: &cgroup.Info{
				CgroupID:  "/cube_sandbox_v2/sandbox/numa0/5",
				UsePoolV2: true,
			},
		}
		anno := sandboxCgroupPathAnnotation(opts)
		require.NotNil(t, anno)
		assert.Equal(t, "/cube_sandbox_v2/sandbox/numa0/5", anno[constants.AnnotationsCgroupPath])
	})

	t.Run("unbound or degraded sandbox carries the v1 pool path", func(t *testing.T) {
		opts := &workflow.CreateContext{
			CgroupInfo: &cgroup.Info{
				CgroupID: "/cube_sandbox/sandbox/7",
			},
		}
		anno := sandboxCgroupPathAnnotation(opts)
		require.NotNil(t, anno)
		assert.Equal(t, "/cube_sandbox/sandbox/7", anno[constants.AnnotationsCgroupPath])
	})

	t.Run("absent CgroupInfo yields no annotation", func(t *testing.T) {
		assert.Nil(t, sandboxCgroupPathAnnotation(&workflow.CreateContext{}))
		assert.Nil(t, sandboxCgroupPathAnnotation(&workflow.CreateContext{
			CgroupInfo: "not-a-cgroup-info",
		}))
	})
}
