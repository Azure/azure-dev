// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

type fakeContainerAppReader struct {
	mu        sync.Mutex
	snapshots []*containerAppSnapshot
	err       error
	calls     int
}

func (r *fakeContainerAppReader) Get(context.Context, string, string) (*containerAppSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	index := min(r.calls, len(r.snapshots)-1)
	r.calls++
	return r.snapshots[index], nil
}

func TestContainerAppResourceResolveUsesPersistedCustomIdentity(t *testing.T) {
	reader := &fakeContainerAppReader{snapshots: []*containerAppSnapshot{{
		ID:       "/subscriptions/sub/resourceGroups/custom-rg/providers/Microsoft.App/containerApps/custom-app",
		Name:     "custom-app",
		Type:     containerAppType,
		Location: "westus3",
		Tags: map[string]*string{
			azdEnvironmentTag: new("dev"),
			azdServiceNameTag: new("petclinic"),
		},
	}}}
	resources := &azureContainerAppResourceService{
		reader: func(context.Context, string) (containerAppReader, error) {
			return reader, nil
		},
	}

	target, err := resources.Resolve(
		t.Context(),
		"sub",
		"dev",
		map[string]string{
			containerAppIDEnv: "/subscriptions/sub/resourceGroups/custom-rg/providers/" +
				"Microsoft.App/containerApps/custom-app",
		},
		&azdext.ServiceConfig{Name: "petclinic"},
		func() (*azdext.TargetResource, error) {
			return nil, errors.New("default resolver must not run")
		},
	)

	require.NoError(t, err)
	require.Equal(t, "custom-rg", target.GetResourceGroupName())
	require.Equal(t, "custom-app", target.GetResourceName())
	require.Equal(t, containerAppType, target.GetResourceType())
	require.Equal(t, "westus3", target.GetMetadata()["location"])
}

func TestContainerAppResourceResolveFailsClosedOnOwnershipAndResolverErrors(t *testing.T) {
	t.Run("ownership", func(t *testing.T) {
		reader := &fakeContainerAppReader{snapshots: []*containerAppSnapshot{{
			ID:   "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.App/containerApps/app",
			Name: "app",
			Type: containerAppType,
			Tags: map[string]*string{
				azdEnvironmentTag: new("other"),
				azdServiceNameTag: new("petclinic"),
			},
		}}}
		resources := &azureContainerAppResourceService{
			reader: func(context.Context, string) (containerAppReader, error) {
				return reader, nil
			},
		}

		_, err := resources.Resolve(
			t.Context(),
			"sub",
			"dev",
			map[string]string{
				containerAppIDEnv: "/subscriptions/sub/resourceGroups/rg/providers/" +
					"Microsoft.App/containerApps/app",
			},
			&azdext.ServiceConfig{Name: "petclinic"},
			nil,
		)
		require.ErrorContains(t, err, "not owned")
	})

	t.Run("default resolver", func(t *testing.T) {
		resources := &azureContainerAppResourceService{}
		_, err := resources.Resolve(
			t.Context(),
			"sub",
			"dev",
			map[string]string{},
			&azdext.ServiceConfig{Name: "petclinic"},
			func() (*azdext.TargetResource, error) {
				return nil, errors.New("resolver https://user:pass@example.test/path?sig=secret#fragment failed")
			},
		)
		require.ErrorContains(t, err, "resolving the default")
		require.NotContains(t, err.Error(), "user")
		require.NotContains(t, err.Error(), "pass")
		require.NotContains(t, err.Error(), "sig=")
	})
}

func TestContainerAppResourceWaitReadyAndEndpoints(t *testing.T) {
	reader := &fakeContainerAppReader{snapshots: []*containerAppSnapshot{
		{
			LatestRevisionName:      "app--2",
			LatestReadyRevisionName: "app--1",
			ProvisioningState:       "Succeeded",
		},
		{
			LatestRevisionName:      "app--2",
			LatestReadyRevisionName: "app--2",
			ProvisioningState:       "Succeeded",
			Fqdn:                    "app.example.test",
		},
	}}
	resources := &azureContainerAppResourceService{
		reader: func(context.Context, string) (containerAppReader, error) {
			return reader, nil
		},
		pollInterval: time.Millisecond,
		readyTimeout: time.Second,
	}
	target := &azdext.TargetResource{
		SubscriptionId:    "sub",
		ResourceGroupName: "rg",
		ResourceName:      "app",
	}

	require.NoError(t, resources.WaitReady(t.Context(), target))
	endpoints, err := resources.Endpoints(t.Context(), target)
	require.NoError(t, err)
	require.Equal(t, []string{"https://app.example.test/"}, endpoints)
}

func TestContainerAppResourceWaitReadyHonorsCancellation(t *testing.T) {
	reader := &fakeContainerAppReader{snapshots: []*containerAppSnapshot{{
		LatestRevisionName:      "app--2",
		LatestReadyRevisionName: "app--1",
		ProvisioningState:       "Succeeded",
	}}}
	resources := &azureContainerAppResourceService{
		reader: func(context.Context, string) (containerAppReader, error) {
			return reader, nil
		},
		pollInterval: time.Hour,
		readyTimeout: time.Hour,
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := resources.WaitReady(ctx, &azdext.TargetResource{
		SubscriptionId:    "sub",
		ResourceGroupName: "rg",
		ResourceName:      "app",
	})
	require.ErrorIs(t, err, context.Canceled)
}
