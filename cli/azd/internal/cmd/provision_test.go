// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/stretchr/testify/require"
)

// TestIsDeploymentSkipped_AllSkipReasons verifies that isDeploymentSkipped
// correctly identifies ALL skip reasons, not just DeploymentStateSkipped.
//
// Regression test for https://github.com/Azure/azure-dev/issues/7305:
// When the user declines preflight validation warnings, Deploy returns
// PreflightAbortedSkipped with a nil Deployment. If this skip reason is not
// detected, the caller dereferences nil Deployment.Outputs and panics.
func TestIsDeploymentSkipped_AllSkipReasons(t *testing.T) {
	tests := []struct {
		name          string
		result        *provisioning.DeployResult
		expectSkipped bool
		nilDeployment bool
	}{
		{
			name: "DeploymentStateSkipped",
			result: &provisioning.DeployResult{
				SkippedReason: provisioning.DeploymentStateSkipped,
			},
			expectSkipped: true,
			nilDeployment: true,
		},
		{
			// This is the regression case from issue #7305.
			// Before the fix, this was NOT detected as skipped, causing a nil
			// pointer dereference when accessing Deployment.Outputs.
			name: "PreflightAbortedSkipped",
			result: &provisioning.DeployResult{
				SkippedReason: provisioning.PreflightAbortedSkipped,
			},
			expectSkipped: true,
			nilDeployment: true,
		},
		{
			name: "NotSkipped_WithDeployment",
			result: &provisioning.DeployResult{
				Deployment: &provisioning.Deployment{
					Outputs: map[string]provisioning.OutputParameter{},
				},
			},
			expectSkipped: false,
			nilDeployment: false,
		},
		{
			name:          "NilDeployResult",
			result:        nil,
			expectSkipped: false,
			nilDeployment: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skipped := isDeploymentSkipped(tt.result)
			require.Equal(t, tt.expectSkipped, skipped,
				"isDeploymentSkipped returned unexpected value")

			if tt.result == nil {
				return
			}

			// Verify the Deployment nil/non-nil state matches expectations.
			// This is important: the bug in #7305 was caused by accessing
			// Deployment.Outputs when Deployment was nil.
			if tt.nilDeployment {
				require.Nil(t, tt.result.Deployment,
					"when skipped, Deployment may be nil (accessing it would panic)")
			} else {
				require.NotNil(t, tt.result.Deployment,
					"when not skipped, Deployment must not be nil (callers access Deployment.Outputs)")
			}
		})
	}
}
