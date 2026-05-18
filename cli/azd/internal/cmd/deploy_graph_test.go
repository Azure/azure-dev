// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"slices"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
	"github.com/azure/azure-dev/cli/azd/pkg/exegraph"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/require"
)

// TestDeployServicesGraph_AspireParallel verifies that the Aspire build-gate
// policy does NOT add graph-level dependency edges between Aspire deploy steps.
// Serialization of the dotnet publish phase is handled at runtime via a mutex
// (see build_gate.go), so all Aspire services deploy in parallel at the graph
// level while only their image-preparation phases are serialized.
func TestDeployServicesGraph_AspireParallel(t *testing.T) {
	services := []*project.ServiceConfig{
		{Name: "api", DotNetContainerApp: &project.DotNetContainerAppOptions{
			Manifest: &apphost.Manifest{},
		}},
		{Name: "worker", DotNetContainerApp: &project.DotNetContainerAppOptions{
			Manifest: &apphost.Manifest{},
		}},
		{Name: "web"}, // non-Aspire
		{Name: "backend", DotNetContainerApp: &project.DotNetContainerAppOptions{
			Manifest: &apphost.Manifest{},
		}},
	}

	g := buildDeployGraph(t, services)

	require.Equal(t, 4, g.Len())

	steps := g.Steps()
	stepMap := make(map[string]*exegraph.Step, len(steps))
	for _, s := range steps {
		stepMap[s.Name] = s
	}

	// All Aspire services should have NO inter-service deploy edges.
	// Build serialization is handled at runtime, not via graph topology.
	require.Empty(t, stepMap["deploy-api"].DependsOn,
		"Aspire service should have no graph-level deploy dependencies")
	require.Empty(t, stepMap["deploy-worker"].DependsOn,
		"Aspire service should have no graph-level deploy dependencies")
	require.Empty(t, stepMap["deploy-backend"].DependsOn,
		"Aspire service should have no graph-level deploy dependencies")

	// Non-Aspire service — also no dependencies.
	require.Empty(t, stepMap["deploy-web"].DependsOn,
		"non-Aspire service should have no dependencies")

	require.NoError(t, g.Validate())
}

// TestDeployServicesDAG_NoAspire verifies that when no services are Aspire
// services, the graph has no inter-service dependency edges at all.
func TestDeployServicesDAG_NoAspire(t *testing.T) {
	services := []*project.ServiceConfig{
		{Name: "api"},
		{Name: "web"},
		{Name: "worker"},
	}

	g := buildDeployGraph(t, services)

	require.Equal(t, 3, g.Len())

	for _, s := range g.Steps() {
		require.Empty(t, s.DependsOn,
			"service %q should have no dependencies in non-Aspire graph", s.Name)
	}

	require.NoError(t, g.Validate())
}

// TestDeployServicesDAG_SingleAspireNoDeps ensures a single Aspire service
// does not depend on itself or anything else.
func TestDeployServicesDAG_SingleAspireNoDeps(t *testing.T) {
	services := []*project.ServiceConfig{
		{Name: "api", DotNetContainerApp: &project.DotNetContainerAppOptions{
			Manifest: &apphost.Manifest{},
		}},
		{Name: "web"},
	}

	g := buildDeployGraph(t, services)

	require.Equal(t, 2, g.Len())

	steps := g.Steps()
	stepMap := make(map[string]*exegraph.Step, len(steps))
	for _, s := range steps {
		stepMap[s.Name] = s
	}

	require.Empty(t, stepMap["deploy-api"].DependsOn,
		"single Aspire service should have no dependencies")
	require.Empty(t, stepMap["deploy-web"].DependsOn,
		"non-Aspire service should have no dependencies")

	require.NoError(t, g.Validate())
}

// TestDeployServicesGraph_UsesDependencies verifies that declared
// `services.<name>.uses:` entries that target other services produce
// deploy-step edges, so hooks that pass values between services retain
// the deploy ordering they had under the old sequential loop. Entries
// that target resources (not services) must be ignored.
func TestDeployServicesGraph_UsesDependencies(t *testing.T) {
	services := []*project.ServiceConfig{
		{Name: "api"},
		{Name: "web", Uses: []string{"api", "cosmos"}}, // cosmos is a resource
		{Name: "worker", Uses: []string{"api"}},
		{Name: "independent"},
	}

	g := buildDeployGraph(t, services)

	require.Equal(t, 4, g.Len())

	steps := g.Steps()
	stepMap := make(map[string]*exegraph.Step, len(steps))
	for _, s := range steps {
		stepMap[s.Name] = s
	}

	require.Empty(t, stepMap["deploy-api"].DependsOn,
		"api has no uses - no deps")
	require.Equal(t, []string{"deploy-api"}, stepMap["deploy-web"].DependsOn,
		"web uses api (service); cosmos (resource) must be ignored")
	require.Equal(t, []string{"deploy-api"}, stepMap["deploy-worker"].DependsOn,
		"worker uses api (service)")
	require.Empty(t, stepMap["deploy-independent"].DependsOn,
		"independent has no uses - no deps")

	require.NoError(t, g.Validate())
}

// TestDeployServicesGraph_UsesWithAspire verifies that `uses:` edges
// still work alongside Aspire services. The build gate no longer adds
// graph edges, so only `uses:` produces deploy-step dependencies.
func TestDeployServicesGraph_UsesWithAspire(t *testing.T) {
	services := []*project.ServiceConfig{
		{Name: "api", DotNetContainerApp: &project.DotNetContainerAppOptions{
			Manifest: &apphost.Manifest{},
		}},
		{Name: "web", Uses: []string{"api"}, DotNetContainerApp: &project.DotNetContainerAppOptions{
			Manifest: &apphost.Manifest{},
		}},
	}

	g := buildDeployGraph(t, services)

	steps := g.Steps()
	stepMap := make(map[string]*exegraph.Step, len(steps))
	for _, s := range steps {
		stepMap[s.Name] = s
	}

	require.Empty(t, stepMap["deploy-api"].DependsOn)
	// web depends on api via `uses:` only (build gate no longer adds edges).
	require.Equal(t, []string{"deploy-api"},
		stepMap["deploy-web"].DependsOn,
		"web should depend on api via uses: edge")

	require.NoError(t, g.Validate())
}

// buildDeployGraph mirrors the deploy-step wiring in
// [addServiceStepsToGraph] without pulling in the full DeployAction wiring,
// so tests can focus on graph topology. Only `uses:` edges between services
// are wired; build-gate serialization is handled at runtime (not in the
// graph), so it is not reflected here.
func buildDeployGraph(
	t *testing.T,
	services []*project.ServiceConfig,
) *exegraph.Graph {
	t.Helper()

	g := exegraph.NewGraph()
	serviceNames := make(map[string]struct{}, len(services))
	for _, svc := range services {
		serviceNames[svc.Name] = struct{}{}
	}

	for _, svc := range services {
		stepName := "deploy-" + svc.Name
		var deps []string

		for _, dep := range svc.Uses {
			if dep == svc.Name {
				continue
			}
			if _, ok := serviceNames[dep]; !ok {
				continue
			}
			depStep := "deploy-" + dep
			if !slices.Contains(deps, depStep) {
				deps = append(deps, depStep)
			}
		}

		err := g.AddStep(&exegraph.Step{
			Name:      stepName,
			DependsOn: deps,
			Tags:      []string{"deploy"},
			Action:    func(_ context.Context) error { return nil },
		})
		require.NoError(t, err, "AddStep(%q) should succeed", stepName)
	}

	return g
}
