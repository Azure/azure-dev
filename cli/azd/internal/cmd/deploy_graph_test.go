// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
	"github.com/azure/azure-dev/cli/azd/pkg/exegraph"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/require"
)

// TestDeployServicesGraph_AspireOrdering verifies that the Aspire build-gate
// policy (supplied by [aspireBuildGateKey]) produces the expected "first wins,
// rest wait" serialization: the first Aspire service has no extra deps, every
// subsequent Aspire service depends on that first step, and non-Aspire
// services run with no inter-service edges.
func TestDeployServicesGraph_AspireOrdering(t *testing.T) {
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

	g := buildDeployGraph(t, services, aspireBuildGateKey)

	require.Equal(t, 4, g.Len())

	steps := g.Steps()
	stepMap := make(map[string]*exegraph.Step, len(steps))
	for _, s := range steps {
		stepMap[s.Name] = s
	}

	// First Aspire service — no dependencies.
	require.Empty(t, stepMap["deploy-api"].DependsOn,
		"first Aspire service should have no dependencies")

	// Second Aspire service — depends on first.
	require.Equal(t, []string{"deploy-api"}, stepMap["deploy-worker"].DependsOn,
		"subsequent Aspire service should depend on the first Aspire service")

	// Third Aspire service — also depends on first.
	require.Equal(t, []string{"deploy-api"}, stepMap["deploy-backend"].DependsOn,
		"subsequent Aspire service should depend on the first Aspire service")

	// Non-Aspire service — no dependencies.
	require.Empty(t, stepMap["deploy-web"].DependsOn,
		"non-Aspire service should have no dependencies")

	// The graph must be valid (no cycles, no missing refs).
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

	g := buildDeployGraph(t, services, aspireBuildGateKey)

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

	g := buildDeployGraph(t, services, aspireBuildGateKey)

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

// TestDeployServicesGraph_BuildGateKeyIsGeneric verifies that the build-gate
// policy is opaque to the graph builder: multiple independent gate keys
// coexist, each serializing only its own group, and services returning "" run
// in full parallelism alongside both groups. No Aspire-specific knowledge is
// required of the graph layer.
func TestDeployServicesGraph_BuildGateKeyIsGeneric(t *testing.T) {
	services := []*project.ServiceConfig{
		{Name: "a1"},
		{Name: "a2"},
		{Name: "b1"},
		{Name: "free"},
		{Name: "a3"},
		{Name: "b2"},
	}

	// Two disjoint gate groups plus an ungated service.
	gate := func(s *project.ServiceConfig) string {
		switch s.Name {
		case "a1", "a2", "a3":
			return "group-a"
		case "b1", "b2":
			return "group-b"
		default:
			return ""
		}
	}

	g := buildDeployGraph(t, services, gate)
	require.Equal(t, 6, g.Len())

	steps := g.Steps()
	stepMap := make(map[string]*exegraph.Step, len(steps))
	for _, s := range steps {
		stepMap[s.Name] = s
	}

	// group-a: a1 is the gate; a2 and a3 depend on a1 only.
	require.Empty(t, stepMap["deploy-a1"].DependsOn)
	require.Equal(t, []string{"deploy-a1"}, stepMap["deploy-a2"].DependsOn)
	require.Equal(t, []string{"deploy-a1"}, stepMap["deploy-a3"].DependsOn)

	// group-b: b1 is the gate; b2 depends on b1 — crucially, NOT on a1.
	require.Empty(t, stepMap["deploy-b1"].DependsOn)
	require.Equal(t, []string{"deploy-b1"}, stepMap["deploy-b2"].DependsOn)

	// Ungated service has no cross-group edges.
	require.Empty(t, stepMap["deploy-free"].DependsOn)

	require.NoError(t, g.Validate())
}

// buildDeployGraph mirrors the deploy-step wiring in
// [addServiceStepsToGraph] without pulling in the full DeployAction wiring,
// so tests can focus on graph topology. The gate policy is injected to match
// the production contract on [serviceGraphOptions.buildGateKey].
func buildDeployGraph(
	t *testing.T,
	services []*project.ServiceConfig,
	buildGateKey func(*project.ServiceConfig) string,
) *exegraph.Graph {
	t.Helper()

	g := exegraph.NewGraph()
	firstByGate := map[string]string{}

	for _, svc := range services {
		stepName := "deploy-" + svc.Name
		var deps []string

		if buildGateKey != nil {
			if key := buildGateKey(svc); key != "" {
				if first, ok := firstByGate[key]; ok {
					deps = append(deps, first)
				} else {
					firstByGate[key] = stepName
				}
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
