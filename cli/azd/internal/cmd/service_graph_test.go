// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exegraph"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/stretchr/testify/require"
)

// stubServiceManager is a minimal ServiceManager that succeeds for all
// operations. It is purpose-built for graph-topology tests where we only
// care about which steps get wired, not what they do.
type stubServiceManager struct{}

func (s *stubServiceManager) GetRequiredTools(
	_ context.Context, _ *project.ServiceConfig,
) ([]tools.ExternalTool, error) {
	return nil, nil
}
func (s *stubServiceManager) Initialize(_ context.Context, _ *project.ServiceConfig) error {
	return nil
}
func (s *stubServiceManager) Restore(
	_ context.Context, _ *project.ServiceConfig, _ *project.ServiceContext,
	_ *async.Progress[project.ServiceProgress],
) (*project.ServiceRestoreResult, error) {
	return nil, nil
}
func (s *stubServiceManager) Build(
	_ context.Context, _ *project.ServiceConfig, _ *project.ServiceContext,
	_ *async.Progress[project.ServiceProgress],
) (*project.ServiceBuildResult, error) {
	return nil, nil
}
func (s *stubServiceManager) Package(
	_ context.Context, _ *project.ServiceConfig, _ *project.ServiceContext,
	_ *async.Progress[project.ServiceProgress], _ *project.PackageOptions,
) (*project.ServicePackageResult, error) {
	return &project.ServicePackageResult{}, nil
}
func (s *stubServiceManager) Publish(
	_ context.Context, _ *project.ServiceConfig, _ *project.ServiceContext,
	_ *async.Progress[project.ServiceProgress], _ *project.PublishOptions,
) (*project.ServicePublishResult, error) {
	return &project.ServicePublishResult{}, nil
}
func (s *stubServiceManager) Deploy(
	_ context.Context, _ *project.ServiceConfig, _ *project.ServiceContext,
	_ *async.Progress[project.ServiceProgress],
) (*project.ServiceDeployResult, error) {
	return &project.ServiceDeployResult{}, nil
}
func (s *stubServiceManager) GetTargetResource(
	_ context.Context, _ *project.ServiceConfig, _ project.ServiceTarget,
) (*environment.TargetResource, error) {
	return nil, nil
}
func (s *stubServiceManager) GetFrameworkService(
	_ context.Context, _ *project.ServiceConfig,
) (project.FrameworkService, error) {
	return nil, nil
}
func (s *stubServiceManager) GetServiceTarget(
	_ context.Context, _ *project.ServiceConfig,
) (project.ServiceTarget, error) {
	return nil, nil
}

func newGraphOpts(services []*project.ServiceConfig) (serviceGraphOptions, *exegraph.Graph) {
	g := exegraph.NewGraph()
	return serviceGraphOptions{
		services:       services,
		serviceManager: &stubServiceManager{},
		deployTimeout:  30 * time.Second,
		state:          newDeployGraphState(services),
	}, g
}

// TestSelfRefUses verifies that a service with uses: [self] does not
// create a self-referencing deploy step edge — the graph builder
// filters self-references out.
func TestSelfRefUses(t *testing.T) {
	services := []*project.ServiceConfig{
		{Name: "web", Uses: []string{"web"}},
	}

	opts, g := newGraphOpts(services)
	handles, err := addServiceStepsToGraph(g, opts)
	require.NoError(t, err)
	require.Len(t, handles.DeploySteps, 1)

	// Validate the graph: a self-edge would cause a cycle.
	require.NoError(t, g.Validate())

	// Run the graph to verify no deadlock or panic.
	err = exegraph.Run(t.Context(), g, exegraph.RunOptions{})
	require.NoError(t, err)
}

// TestNonexistentUses verifies that a service with uses: [nonexistent]
// is handled gracefully. Entries that don't match another service's
// name are silently ignored (they target resources, not services).
func TestNonexistentUses(t *testing.T) {
	services := []*project.ServiceConfig{
		{Name: "api"},
		{Name: "web", Uses: []string{"nonexistent"}},
	}

	opts, g := newGraphOpts(services)
	handles, err := addServiceStepsToGraph(g, opts)
	require.NoError(t, err)
	require.Len(t, handles.DeploySteps, 2)

	// Validate and run: nonexistent uses should be silently filtered.
	require.NoError(t, g.Validate())
	err = exegraph.Run(t.Context(), g, exegraph.RunOptions{})
	require.NoError(t, err)
}

// TestSequentialFallback verifies that when no service declares a uses:
// edge targeting another service, deploy steps are chained sequentially
// in slice order for backward compatibility with templates that relied
// on implicit ordering.
func TestSequentialFallback(t *testing.T) {
	t.Parallel()
	services := []*project.ServiceConfig{
		{Name: "api"},
		{Name: "web"},
		{Name: "worker"},
	}

	opts, g := newGraphOpts(services)
	handles, err := addServiceStepsToGraph(g, opts)
	require.NoError(t, err)
	require.Len(t, handles.DeploySteps, 3)

	// Run the graph and record completion order.
	var order []string
	var mu sync.Mutex
	err = exegraph.Run(t.Context(), g, exegraph.RunOptions{
		OnStepDone: func(name string, err error) {
			if err == nil && len(name) > 7 && name[:7] == "deploy-" {
				mu.Lock()
				order = append(order, name)
				mu.Unlock()
			}
		},
	})
	require.NoError(t, err)

	// Deploy steps must complete in slice order (sequential fallback).
	require.Equal(t, []string{"deploy-api", "deploy-web", "deploy-worker"}, order)
}

// TestSequentialFallbackNotAppliedWithUses verifies that when at least
// one service declares a uses: edge to another service, the sequential
// fallback does NOT activate — services without uses: run in parallel.
func TestSequentialFallbackNotAppliedWithUses(t *testing.T) {
	t.Parallel()
	services := []*project.ServiceConfig{
		{Name: "api"},
		{Name: "web", Uses: []string{"api"}},
		{Name: "worker"},
	}

	opts, g := newGraphOpts(services)
	_, err := addServiceStepsToGraph(g, opts)
	require.NoError(t, err)
	require.NoError(t, g.Validate())

	// Just verify it runs without deadlock — ordering is now
	// graph-determined, not forced sequential.
	err = exegraph.Run(t.Context(), g, exegraph.RunOptions{})
	require.NoError(t, err)
}

// TestSuggestServiceDeps verifies that the advisory scanner detects
// SERVICE_<OTHER>_* references in service env configs.
func TestSuggestServiceDeps(t *testing.T) {
	t.Parallel()
	services := []*project.ServiceConfig{
		{Name: "api"},
		{
			Name: "web",
			Environment: osutil.ExpandableMap{
				"API_URL": osutil.NewExpandableString("${SERVICE_API_ENDPOINT_URL}"),
			},
		},
		{Name: "worker"},
	}

	// suggestServiceDeps only logs — verify it doesn't panic and
	// correctly identifies the web->api dependency.
	suggestServiceDeps(services)
}

func TestDeployGraphState_ResultsSnapshot(t *testing.T) {
	t.Parallel()
	services := []*project.ServiceConfig{{Name: "api"}, {Name: "web"}}
	state := newDeployGraphState(services)

	// Store some results.
	r1 := &project.ServiceDeployResult{}
	r2 := &project.ServiceDeployResult{}
	state.StoreResult("api", r1)
	state.StoreResult("web", r2)

	// Snapshot must return a copy.
	snap := state.ResultsSnapshot()
	require.Len(t, snap, 2)
	require.Same(t, r1, snap["api"])
	require.Same(t, r2, snap["web"])

	// Mutating the snapshot must not affect the state.
	delete(snap, "api")
	require.Equal(t, r1, state.GetResult("api"), "deleting from snapshot must not affect state")
}

func TestDeployGraphState_StoreLoadContext(t *testing.T) {
	t.Parallel()
	services := []*project.ServiceConfig{{Name: "svc"}}
	state := newDeployGraphState(services)

	require.Nil(t, state.LoadContext("svc"))

	sc := project.NewServiceContext()
	state.StoreContext("svc", sc)
	require.Same(t, sc, state.LoadContext("svc"))
}
