// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsActiveDeploymentState(t *testing.T) {
	active := []DeploymentProvisioningState{
		DeploymentProvisioningStateAccepted,
		DeploymentProvisioningStateCanceling,
		DeploymentProvisioningStateCreating,
		DeploymentProvisioningStateDeleting,
		DeploymentProvisioningStateDeletingResources,
		DeploymentProvisioningStateDeploying,
		DeploymentProvisioningStateRunning,
		DeploymentProvisioningStateUpdating,
		DeploymentProvisioningStateUpdatingDenyAssignments,
		DeploymentProvisioningStateValidating,
		DeploymentProvisioningStateWaiting,
	}

	for _, state := range active {
		t.Run(string(state), func(t *testing.T) {
			require.True(t, IsActiveDeploymentState(state),
				"expected %s to be active", state)
		})
	}

	inactive := []DeploymentProvisioningState{
		DeploymentProvisioningStateSucceeded,
		DeploymentProvisioningStateFailed,
		DeploymentProvisioningStateCanceled,
		DeploymentProvisioningStateDeleted,
		DeploymentProvisioningStateNotSpecified,
		DeploymentProvisioningStateReady,
	}

	for _, state := range inactive {
		t.Run(string(state), func(t *testing.T) {
			require.False(t, IsActiveDeploymentState(state),
				"expected %s to be inactive", state)
		})
	}
}
