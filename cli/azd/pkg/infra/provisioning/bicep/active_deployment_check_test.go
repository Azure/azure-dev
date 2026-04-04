// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

// activeDeploymentScope is a test helper that implements infra.Scope and lets
// the caller control what ListDeployments returns on each call.
type activeDeploymentScope struct {
	// calls tracks how many times ListDeployments has been invoked.
	calls atomic.Int32
	// activePerCall maps a 0-based call index to the list of deployments
	// returned for that call.  If the index is missing, nil is returned.
	activePerCall map[int][]*azapi.ResourceDeployment
	// errOnCall, if non-nil, maps a call index to an error to return.
	errOnCall map[int]error
}

func (s *activeDeploymentScope) SubscriptionId() string { return "test-sub" }

func (s *activeDeploymentScope) Deployment(_ string) infra.Deployment { return nil }

func (s *activeDeploymentScope) ListDeployments(
	_ context.Context,
) ([]*azapi.ResourceDeployment, error) {
	idx := int(s.calls.Add(1)) - 1
	if s.errOnCall != nil {
		if e, ok := s.errOnCall[idx]; ok {
			return nil, e
		}
	}
	if s.activePerCall != nil {
		return s.activePerCall[idx], nil
	}
	return nil, nil
}

// newTestProvider returns a BicepProvider with fast poll settings for tests.
func newTestProvider() *BicepProvider {
	return &BicepProvider{
		console:                  mockinput.NewMockConsole(),
		activeDeployPollInterval: 10 * time.Millisecond,
		activeDeployTimeout:      2 * time.Second,
	}
}

func TestWaitForActiveDeployments_NoActive(t *testing.T) {
	scope := &activeDeploymentScope{}
	p := newTestProvider()

	err := p.waitForActiveDeployments(t.Context(), scope, "test-deploy")
	require.NoError(t, err)
	require.Equal(t, int32(1), scope.calls.Load(),
		"should call ListDeployments once")
}

func TestWaitForActiveDeployments_InitialListError_NotFound(t *testing.T) {
	scope := &activeDeploymentScope{
		errOnCall: map[int]error{
			0: fmt.Errorf("listing: %w", infra.ErrDeploymentsNotFound),
		},
	}
	p := newTestProvider()

	// ErrDeploymentsNotFound (resource group doesn't exist yet) is safe to ignore.
	err := p.waitForActiveDeployments(t.Context(), scope, "test-deploy")
	require.NoError(t, err)
}

func TestWaitForActiveDeployments_InitialListError_Other(t *testing.T) {
	scope := &activeDeploymentScope{
		errOnCall: map[int]error{
			0: fmt.Errorf("auth failure: access denied"),
		},
	}
	p := newTestProvider()

	// Non-NotFound errors are logged and skipped — the check is best-effort.
	err := p.waitForActiveDeployments(t.Context(), scope, "test-deploy")
	require.NoError(t, err)
}

func TestWaitForActiveDeployments_ActiveThenClear(t *testing.T) {
	running := []*azapi.ResourceDeployment{
		{
			Name:              "deploy-1",
			ProvisioningState: azapi.DeploymentProvisioningStateRunning,
		},
	}
	scope := &activeDeploymentScope{
		activePerCall: map[int][]*azapi.ResourceDeployment{
			0: running, // first call: active
			// second call (index 1): missing key → returns nil (no active)
		},
	}
	p := newTestProvider()

	err := p.waitForActiveDeployments(t.Context(), scope, "deploy-1")
	require.NoError(t, err)
	require.Equal(t, int32(2), scope.calls.Load(),
		"should poll once, then see clear")
}

func TestWaitForActiveDeployments_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())

	running := []*azapi.ResourceDeployment{
		{
			Name:              "deploy-forever",
			ProvisioningState: azapi.DeploymentProvisioningStateRunning,
		},
	}
	scope := &activeDeploymentScope{
		// Always return active deployments.
		// Seed multiple indices so a tick before ctx.Done doesn't hit a missing key.
		activePerCall: map[int][]*azapi.ResourceDeployment{
			0: running,
			1: running,
			2: running,
			3: running,
			4: running,
		},
	}
	p := newTestProvider()

	// Cancel immediately so the wait loop exits on the first select.
	cancel()

	err := p.waitForActiveDeployments(ctx, scope, "deploy-forever")
	require.ErrorIs(t, err, context.Canceled)
}

func TestWaitForActiveDeployments_PollError(t *testing.T) {
	running := []*azapi.ResourceDeployment{
		{
			Name:              "deploy-1",
			ProvisioningState: azapi.DeploymentProvisioningStateRunning,
		},
	}
	scope := &activeDeploymentScope{
		activePerCall: map[int][]*azapi.ResourceDeployment{
			0: running,
		},
		errOnCall: map[int]error{
			1: fmt.Errorf("transient ARM failure"),
		},
	}
	p := newTestProvider()

	err := p.waitForActiveDeployments(t.Context(), scope, "deploy-1")
	// Transient poll errors are logged and treated as cleared.
	require.NoError(t, err)
}

func TestWaitForActiveDeployments_PollNotFound(t *testing.T) {
	// If the resource group is deleted externally while polling,
	// ListDeployments returns ErrDeploymentsNotFound. The wait should
	// treat this as "no active deployments" and return nil.
	running := []*azapi.ResourceDeployment{
		{
			Name:              "deploy-1",
			ProvisioningState: azapi.DeploymentProvisioningStateRunning,
		},
	}
	scope := &activeDeploymentScope{
		activePerCall: map[int][]*azapi.ResourceDeployment{
			0: running,
		},
		errOnCall: map[int]error{
			1: infra.ErrDeploymentsNotFound,
		},
	}
	p := newTestProvider()

	err := p.waitForActiveDeployments(t.Context(), scope, "deploy-1")
	require.NoError(t, err)
}

func TestWaitForActiveDeployments_Timeout(t *testing.T) {
	running := []*azapi.ResourceDeployment{
		{
			Name:              "stuck-deploy",
			ProvisioningState: azapi.DeploymentProvisioningStateRunning,
		},
	}
	// Return active on every call.
	perCall := make(map[int][]*azapi.ResourceDeployment)
	for i := range 200 {
		perCall[i] = running
	}

	scope := &activeDeploymentScope{activePerCall: perCall}
	p := &BicepProvider{
		console:                  mockinput.NewMockConsole(),
		activeDeployPollInterval: 5 * time.Millisecond,
		activeDeployTimeout:      50 * time.Millisecond,
	}

	err := p.waitForActiveDeployments(t.Context(), scope, "stuck-deploy")
	require.Error(t, err)
	require.Contains(t, err.Error(), "timed out")
	require.Contains(t, err.Error(), "stuck-deploy")
}

func TestWaitForActiveDeployments_DifferentNameNotBlocked(t *testing.T) {
	running := []*azapi.ResourceDeployment{{
		Name:              "other-deploy",
		ProvisioningState: azapi.DeploymentProvisioningStateRunning,
	}}
	scope := &activeDeploymentScope{
		activePerCall: map[int][]*azapi.ResourceDeployment{0: running},
	}
	p := newTestProvider()
	err := p.waitForActiveDeployments(t.Context(), scope, "my-deploy")
	require.NoError(t, err)
	require.Equal(t, int32(1), scope.calls.Load())
}
