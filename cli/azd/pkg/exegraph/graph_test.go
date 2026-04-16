// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exegraph

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func noop(_ context.Context) error { return nil }

func TestAddStep_Success(t *testing.T) {
	g := NewGraph()
	err := g.AddStep(&Step{Name: "a", Action: noop})
	require.NoError(t, err)
	assert.Equal(t, 1, g.Len())
}

func TestAddStep_EmptyName(t *testing.T) {
	g := NewGraph()
	err := g.AddStep(&Step{Name: "", Action: noop})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not be empty")
}

func TestAddStep_NilStep(t *testing.T) {
	g := NewGraph()
	err := g.AddStep(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not be nil")
}

func TestAddStep_NilAction(t *testing.T) {
	g := NewGraph()
	err := g.AddStep(&Step{Name: "a", Action: nil})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil Action")
}

func TestAddStep_DuplicateName(t *testing.T) {
	g := NewGraph()
	require.NoError(t, g.AddStep(&Step{Name: "a", Action: noop}))
	err := g.AddStep(&Step{Name: "a", Action: noop})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}

func TestSteps_InsertionOrder(t *testing.T) {
	g := NewGraph()
	for _, name := range []string{"c", "a", "b"} {
		require.NoError(t, g.AddStep(&Step{Name: name, Action: noop}))
	}
	steps := g.Steps()
	require.Len(t, steps, 3)
	assert.Equal(t, "c", steps[0].Name)
	assert.Equal(t, "a", steps[1].Name)
	assert.Equal(t, "b", steps[2].Name)
}

func TestStepsByTag(t *testing.T) {
	g := NewGraph()
	require.NoError(t, g.AddStep(&Step{
		Name: "a", Tags: []string{"deploy"}, Action: noop,
	}))
	require.NoError(t, g.AddStep(&Step{
		Name: "b", Tags: []string{"provision"}, Action: noop,
	}))
	require.NoError(t, g.AddStep(&Step{
		Name: "c", Tags: []string{"deploy"}, Action: noop,
	}))

	deploys := g.stepsByTag("deploy")
	require.Len(t, deploys, 2)
	assert.Equal(t, "a", deploys[0].Name)
	assert.Equal(t, "c", deploys[1].Name)
}

func TestValidate_NoCycle(t *testing.T) {
	g := NewGraph()
	require.NoError(t, g.AddStep(&Step{Name: "a", Action: noop}))
	require.NoError(t, g.AddStep(&Step{
		Name: "b", DependsOn: []string{"a"}, Action: noop,
	}))
	require.NoError(t, g.AddStep(&Step{
		Name: "c", DependsOn: []string{"b"}, Action: noop,
	}))
	assert.NoError(t, g.Validate())
}

func TestValidate_CycleDetected(t *testing.T) {
	g := NewGraph()
	require.NoError(t, g.AddStep(&Step{
		Name: "a", DependsOn: []string{"c"}, Action: noop,
	}))
	require.NoError(t, g.AddStep(&Step{
		Name: "b", DependsOn: []string{"a"}, Action: noop,
	}))
	require.NoError(t, g.AddStep(&Step{
		Name: "c", DependsOn: []string{"b"}, Action: noop,
	}))
	err := g.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cycle")
}

func TestValidate_SelfCycle(t *testing.T) {
	g := NewGraph()
	require.NoError(t, g.AddStep(&Step{
		Name: "a", DependsOn: []string{"a"}, Action: noop,
	}))
	err := g.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cycle")
}

func TestValidate_MissingDependency(t *testing.T) {
	g := NewGraph()
	require.NoError(t, g.AddStep(&Step{
		Name: "a", DependsOn: []string{"ghost"}, Action: noop,
	}))
	err := g.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
}

func TestPriority_LinearChain(t *testing.T) {
	// a → b → c: a has 2 transitive deps, b has 1, c has 0.
	g := NewGraph()
	require.NoError(t, g.AddStep(&Step{Name: "a", Action: noop}))
	require.NoError(t, g.AddStep(&Step{Name: "b", DependsOn: []string{"a"}, Action: noop}))
	require.NoError(t, g.AddStep(&Step{Name: "c", DependsOn: []string{"b"}, Action: noop}))

	assert.Equal(t, 2, g.Priority("a"))
	assert.Equal(t, 1, g.Priority("b"))
	assert.Equal(t, 0, g.Priority("c"))
}

func TestPriority_Diamond(t *testing.T) {
	// a → b, a → c, b → d, c → d
	// a=3 (b,c,d), b=1 (d), c=1 (d), d=0
	g := NewGraph()
	require.NoError(t, g.AddStep(&Step{Name: "a", Action: noop}))
	require.NoError(t, g.AddStep(&Step{Name: "b", DependsOn: []string{"a"}, Action: noop}))
	require.NoError(t, g.AddStep(&Step{Name: "c", DependsOn: []string{"a"}, Action: noop}))
	require.NoError(t, g.AddStep(&Step{Name: "d", DependsOn: []string{"b", "c"}, Action: noop}))

	assert.Equal(t, 3, g.Priority("a"))
	assert.Equal(t, 1, g.Priority("b"))
	assert.Equal(t, 1, g.Priority("c"))
	assert.Equal(t, 0, g.Priority("d"))
}

func TestPriorityOrder_CriticalPathFirst(t *testing.T) {
	// Simulate a provision→deploy graph:
	//   provision-0 (3 deps) → publish-a (1 dep), publish-b (1 dep)
	//   publish-a → deploy-a (0 deps)
	//   publish-b → deploy-b (0 deps)
	//   package-a, package-b (0 deps each — no dependents)
	g := NewGraph()
	require.NoError(t, g.AddStep(&Step{Name: "package-a", Action: noop}))
	require.NoError(t, g.AddStep(&Step{Name: "package-b", Action: noop}))
	require.NoError(t, g.AddStep(&Step{Name: "provision-0", Action: noop}))
	require.NoError(t, g.AddStep(&Step{Name: "publish-a", DependsOn: []string{"package-a", "provision-0"}, Action: noop}))
	require.NoError(t, g.AddStep(&Step{Name: "publish-b", DependsOn: []string{"package-b", "provision-0"}, Action: noop}))
	require.NoError(t, g.AddStep(&Step{Name: "deploy-a", DependsOn: []string{"publish-a"}, Action: noop}))
	require.NoError(t, g.AddStep(&Step{Name: "deploy-b", DependsOn: []string{"publish-b"}, Action: noop}))

	order := g.priorityOrder()
	// provision-0 has 4 transitive dependents (publish-a, publish-b, deploy-a, deploy-b)
	// It should appear before package-a and package-b (which have 0-2 dependents).
	assert.Equal(t, "provision-0", order[0], "provision should be scheduled first (most dependents)")
}

func TestPriority_IndependentSteps(t *testing.T) {
	// All independent steps should have priority 0.
	g := NewGraph()
	require.NoError(t, g.AddStep(&Step{Name: "a", Action: noop}))
	require.NoError(t, g.AddStep(&Step{Name: "b", Action: noop}))
	require.NoError(t, g.AddStep(&Step{Name: "c", Action: noop}))

	assert.Equal(t, 0, g.Priority("a"))
	assert.Equal(t, 0, g.Priority("b"))
	assert.Equal(t, 0, g.Priority("c"))
}
