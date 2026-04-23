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
	var svcCtxMu, resultsMu sync.Mutex
	return serviceGraphOptions{
		services:        services,
		serviceManager:  &stubServiceManager{},
		deployTimeout:   30 * time.Second,
		serviceContexts: make(map[string]*project.ServiceContext),
		svcCtxMu:        &svcCtxMu,
		deployResults:   make(map[string]*project.ServiceDeployResult),
		resultsMu:       &resultsMu,
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
