// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/stretchr/testify/require"
)

func TestIsTerminalProvisioningState(t *testing.T) {
	terminal := []azapi.DeploymentProvisioningState{
		azapi.DeploymentProvisioningStateCanceled,
		azapi.DeploymentProvisioningStateFailed,
		azapi.DeploymentProvisioningStateSucceeded,
	}
	nonTerminal := []azapi.DeploymentProvisioningState{
		azapi.DeploymentProvisioningStateAccepted,
		azapi.DeploymentProvisioningStateCanceling,
		azapi.DeploymentProvisioningStateRunning,
		azapi.DeploymentProvisioningStateDeploying,
		azapi.DeploymentProvisioningStateValidating,
		azapi.DeploymentProvisioningStateWaiting,
		azapi.DeploymentProvisioningStateNotSpecified,
		"",
	}
	for _, s := range terminal {
		require.Truef(t, isTerminalProvisioningState(s), "expected %q to be terminal", s)
	}
	for _, s := range nonTerminal {
		require.Falsef(t, isTerminalProvisioningState(s), "expected %q to NOT be terminal", s)
	}
}

func TestApplyInterruptOutcome(t *testing.T) {
	leave := interruptOutcome{
		err:            provisioning.ErrDeploymentInterruptedLeaveRunning,
		telemetryValue: "leave_running",
	}
	canceled := interruptOutcome{
		err:            provisioning.ErrDeploymentCanceledByUser,
		telemetryValue: "canceled",
	}

	t.Run("nil deploy error returns outcome err", func(t *testing.T) {
		require.ErrorIs(t, applyInterruptOutcome(leave, nil),
			provisioning.ErrDeploymentInterruptedLeaveRunning)
		require.ErrorIs(t, applyInterruptOutcome(canceled, nil),
			provisioning.ErrDeploymentCanceledByUser)
	})

	t.Run("context canceled is replaced by outcome err", func(t *testing.T) {
		err := applyInterruptOutcome(canceled, context.Canceled)
		require.ErrorIs(t, err, provisioning.ErrDeploymentCanceledByUser)
		require.NotErrorIs(t, err, context.Canceled)
	})

	t.Run("wrapped context canceled is replaced by outcome err", func(t *testing.T) {
		wrapped := fmt.Errorf("PollUntilDone: %w", context.Canceled)
		err := applyInterruptOutcome(leave, wrapped)
		require.ErrorIs(t, err, provisioning.ErrDeploymentInterruptedLeaveRunning)
		require.NotErrorIs(t, err, context.Canceled)
	})

	t.Run("non-cancel deploy error is preserved alongside outcome", func(t *testing.T) {
		other := errors.New("template validation failed")
		err := applyInterruptOutcome(canceled, other)
		require.ErrorIs(t, err, provisioning.ErrDeploymentCanceledByUser)
		require.ErrorIs(t, err, other)
	})
}
