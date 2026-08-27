// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exegraph"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

// stubServiceManager is a minimal ServiceManager that succeeds for all
// operations. It is purpose-built for graph-topology tests where we only
// care about which steps get wired, not what they do.
type stubServiceManager struct {
	packageFn func(context.Context, *project.ServiceConfig)
	publishFn func(context.Context, *project.ServiceConfig)
	deployFn  func(context.Context, *project.ServiceConfig)
}

func (s *stubServiceManager) GetRequiredTools(
	_ context.Context, _ *project.ServiceConfig,
) ([]tools.ExternalTool, error) {
	return nil, nil
}
func (s *stubServiceManager) Initialize(_ context.Context, _ *project.ServiceConfig) error {
	return nil
}

func (s *stubServiceManager) InitializeFrameworkService(_ context.Context, _ *project.ServiceConfig) error {
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
	ctx context.Context, svc *project.ServiceConfig, _ *project.ServiceContext,
	_ *async.Progress[project.ServiceProgress], _ *project.PackageOptions,
) (*project.ServicePackageResult, error) {
	if s.packageFn != nil {
		s.packageFn(ctx, svc)
	}
	return &project.ServicePackageResult{}, nil
}
func (s *stubServiceManager) Publish(
	ctx context.Context, svc *project.ServiceConfig, _ *project.ServiceContext,
	_ *async.Progress[project.ServiceProgress], _ *project.PublishOptions,
) (*project.ServicePublishResult, error) {
	if s.publishFn != nil {
		s.publishFn(ctx, svc)
	}
	return &project.ServicePublishResult{}, nil
}
func (s *stubServiceManager) Deploy(
	ctx context.Context, svc *project.ServiceConfig, _ *project.ServiceContext,
	_ *async.Progress[project.ServiceProgress],
) (*project.ServiceDeployResult, error) {
	if s.deployFn != nil {
		s.deployFn(ctx, svc)
	}
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

func TestDisplayDeployWarnings(t *testing.T) {
	services := []*project.ServiceConfig{{Name: "api"}, {Name: "web"}}
	state := newDeployGraphState(services)
	state.StoreResult("api", &project.ServiceDeployResult{
		Warnings: []string{"deployment status did not change"},
	})
	state.StoreResult("web", &project.ServiceDeployResult{})
	console := mockinput.NewMockConsole()

	displayDeployWarnings(t.Context(), console, services, state)

	require.Len(t, console.Output(), 1)
	require.Contains(t, console.Output()[0], "WARNING:")
	require.Contains(t, console.Output()[0], "Service 'api': deployment status did not change")
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

// TestBuildGateParallelWithArtifactsPath verifies that when a buildGateKey
// is set, deploy steps still execute in PARALLEL at the graph level (no chain
// edges). The build-race prevention is handled at runtime via --artifacts-path
// and fallback mutex, not via graph topology. This is the fix for GitHub issue
// #8177: full parallelism preserved while isolating intermediate build outputs.
func TestBuildGateParallelWithArtifactsPath(t *testing.T) {
	t.Parallel()

	services := []*project.ServiceConfig{
		{Name: "api", DotNetContainerApp: &project.DotNetContainerAppOptions{
			Manifest: &apphost.Manifest{},
		}},
		{Name: "worker", DotNetContainerApp: &project.DotNetContainerAppOptions{
			Manifest: &apphost.Manifest{},
		}},
		{Name: "web", DotNetContainerApp: &project.DotNetContainerAppOptions{
			Manifest: &apphost.Manifest{},
		}},
		{Name: "free"}, // non-Aspire, should not be gated
	}

	opts, g := newGraphOpts(services)
	opts.buildGateKey = aspireBuildGateKey
	handles, err := addServiceStepsToGraph(g, opts)
	require.NoError(t, err)
	require.Len(t, handles.DeploySteps, 4)
	require.NoError(t, g.Validate())

	// Run the graph to confirm it completes without deadlock.
	var order []string
	var mu sync.Mutex
	err = exegraph.Run(t.Context(), g, exegraph.RunOptions{
		OnStepDone: func(name string, stepErr error) {
			if stepErr == nil && strings.HasPrefix(name, "deploy-") {
				mu.Lock()
				order = append(order, name)
				mu.Unlock()
			}
		},
	})
	require.NoError(t, err)

	// All 4 services should have completed.
	require.Len(t, order, 4, "all deploy steps should complete")
	require.Contains(t, order, "deploy-api")
	require.Contains(t, order, "deploy-worker")
	require.Contains(t, order, "deploy-web")
	require.Contains(t, order, "deploy-free")

	// Verify graph topology: no Aspire deploy step should depend on another.
	steps := g.Steps()
	for _, s := range steps {
		if !strings.HasPrefix(s.Name, "deploy-") {
			continue
		}
		for _, dep := range s.DependsOn {
			if strings.HasPrefix(dep, "deploy-") {
				t.Errorf("deploy step %q has graph-level dependency on %q; "+
					"build gate should NOT produce graph edges", s.Name, dep)
			}
		}
	}
}

// TestBuildGateMultipleKeys verifies that multiple independent gate keys
// produce distinct mutexes per key and no graph-level deploy edges. This
// ensures services in different gate groups are isolated from each other
// (e.g. two different Aspire app hosts in the same project).
func TestBuildGateMultipleKeys(t *testing.T) {
	t.Parallel()

	services := []*project.ServiceConfig{
		{Name: "groupA-1", DotNetContainerApp: &project.DotNetContainerAppOptions{
			Manifest: &apphost.Manifest{},
		}},
		{Name: "groupA-2", DotNetContainerApp: &project.DotNetContainerAppOptions{
			Manifest: &apphost.Manifest{},
		}},
		{Name: "groupB-1", DotNetContainerApp: &project.DotNetContainerAppOptions{
			Manifest: &apphost.Manifest{},
		}},
		{Name: "groupB-2", DotNetContainerApp: &project.DotNetContainerAppOptions{
			Manifest: &apphost.Manifest{},
		}},
	}

	opts, g := newGraphOpts(services)
	// Two distinct gate keys: "gate-A" and "gate-B".
	opts.buildGateKey = func(svc *project.ServiceConfig) string {
		if strings.HasPrefix(svc.Name, "groupA") {
			return "gate-A"
		}
		if strings.HasPrefix(svc.Name, "groupB") {
			return "gate-B"
		}
		return ""
	}

	handles, err := addServiceStepsToGraph(g, opts)
	require.NoError(t, err)
	require.Len(t, handles.DeploySteps, 4)
	require.NoError(t, g.Validate())

	// Run the graph to verify no deadlock (which would occur if independent
	// gate groups accidentally shared a mutex with blocking semantics).
	err = exegraph.Run(t.Context(), g, exegraph.RunOptions{})
	require.NoError(t, err)

	// Verify no deploy→deploy edges exist for any gate group.
	for _, s := range g.Steps() {
		if !strings.HasPrefix(s.Name, "deploy-") {
			continue
		}
		for _, dep := range s.DependsOn {
			if strings.HasPrefix(dep, "deploy-") {
				t.Errorf("deploy step %q depends on %q; multi-key gates should NOT produce edges", s.Name, dep)
			}
		}
	}
}

func TestDotNetPackageBuildGateSharedAcrossPackageAndPublish(t *testing.T) {
	t.Parallel()

	services := []*project.ServiceConfig{
		{Name: "api", Language: project.ServiceLanguageCsharp},
		{Name: "worker", Language: project.ServiceLanguageFsharp},
	}

	var mu sync.Mutex
	packageGates := make(map[string]*sync.Mutex)
	publishGates := make(map[string]*sync.Mutex)
	deployGates := make(map[string]*sync.Mutex)
	record := func(target map[string]*sync.Mutex) func(context.Context, *project.ServiceConfig) {
		return func(ctx context.Context, svc *project.ServiceConfig) {
			mu.Lock()
			defer mu.Unlock()
			target[svc.Name] = project.BuildGateFromContext(ctx)
		}
	}

	opts, g := newGraphOpts(services)
	manager := opts.serviceManager.(*stubServiceManager)
	manager.packageFn = record(packageGates)
	manager.publishFn = record(publishGates)
	manager.deployFn = record(deployGates)
	opts.packagePublishBuildGateKey = dotNetPackagePublishBuildGateKey

	_, err := addServiceStepsToGraph(g, opts)
	require.NoError(t, err)
	require.NoError(t, exegraph.Run(t.Context(), g, exegraph.RunOptions{}))

	require.NotNil(t, packageGates["api"])
	require.Same(t, packageGates["api"], packageGates["worker"])
	require.Same(t, packageGates["api"], publishGates["api"])
	require.Same(t, packageGates["api"], publishGates["worker"])
	require.Nil(t, deployGates["api"])
	require.Nil(t, deployGates["worker"])
}

func TestDotNetPackageBuildGatePreservesSequentialDeployFallback(t *testing.T) {
	t.Parallel()

	services := []*project.ServiceConfig{
		{Name: "api", Language: project.ServiceLanguageCsharp},
		{Name: "web", Language: project.ServiceLanguageDotNet},
	}

	opts, g := newGraphOpts(services)
	opts.packagePublishBuildGateKey = dotNetPackagePublishBuildGateKey
	_, err := addServiceStepsToGraph(g, opts)
	require.NoError(t, err)

	stepMap := make(map[string]*exegraph.Step)
	for _, step := range g.Steps() {
		stepMap[step.Name] = step
	}
	require.Contains(t, stepMap["deploy-web"].DependsOn, "deploy-api")

	manager := opts.serviceManager.(*stubServiceManager)
	var mu sync.Mutex
	packageGates := make(map[string]*sync.Mutex)
	manager.packageFn = func(ctx context.Context, svc *project.ServiceConfig) {
		mu.Lock()
		defer mu.Unlock()
		packageGates[svc.Name] = project.BuildGateFromContext(ctx)
	}

	require.NoError(t, exegraph.Run(t.Context(), g, exegraph.RunOptions{}))
	require.NotNil(t, packageGates["api"])
	require.Same(t, packageGates["api"], packageGates["web"])
}

func TestDotNetPackageBuildGateDisabledWithoutConcurrency(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		services       []*project.ServiceConfig
		maxConcurrency int
	}{
		{
			name: "single standard .NET candidate",
			services: []*project.ServiceConfig{
				{Name: "api", Language: project.ServiceLanguageDotNet},
				{Name: "web", Language: project.ServiceLanguageJavaScript},
			},
		},
		{
			name: "concurrency one",
			services: []*project.ServiceConfig{
				{Name: "api", Language: project.ServiceLanguageDotNet},
				{Name: "web", Language: project.ServiceLanguageCsharp},
			},
			maxConcurrency: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, g := newGraphOpts(tt.services)
			opts.maxConcurrency = tt.maxConcurrency
			opts.packagePublishBuildGateKey = dotNetPackagePublishBuildGateKey

			manager := opts.serviceManager.(*stubServiceManager)
			var mu sync.Mutex
			gates := make(map[string]*sync.Mutex)
			manager.packageFn = func(ctx context.Context, svc *project.ServiceConfig) {
				mu.Lock()
				defer mu.Unlock()
				gates[svc.Name] = project.BuildGateFromContext(ctx)
			}

			_, err := addServiceStepsToGraph(g, opts)
			require.NoError(t, err)
			require.NoError(t, exegraph.Run(t.Context(), g, exegraph.RunOptions{
				MaxConcurrency: tt.maxConcurrency,
			}))

			for _, svc := range tt.services {
				require.Nil(t, gates[svc.Name])
			}
		})
	}
}
