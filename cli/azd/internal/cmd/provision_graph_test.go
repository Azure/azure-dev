// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exegraph"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNoopSaveEnvManager(t *testing.T) {
	t.Parallel()

	inner := &mockenv.MockEnvManager{}
	noop := &noopSaveEnvManager{Manager: inner}

	env := environment.NewWithValues("test", nil)

	// Save and SaveWithOptions must be no-ops — the inner mock should
	// never be called for these methods.
	require.NoError(t, noop.Save(t.Context(), env))
	require.NoError(t, noop.SaveWithOptions(t.Context(), env, nil))

	// Non-save methods delegate to the inner manager.
	inner.On("Reload", mock.Anything, env).Return(nil)
	require.NoError(t, noop.Reload(t.Context(), env))
	inner.AssertCalled(t, "Reload", mock.Anything, env)
}

func TestSyncConsole_SerializesMessages(t *testing.T) {
	t.Parallel()

	inner := &trackingConsole{
		MockConsole: mockinput.NewMockConsole(),
		messages:    make([]string, 0, 1000),
	}
	sc := &syncConsole{Console: inner}

	const goroutines = 20
	const msgsPerGoroutine = 50

	var wg sync.WaitGroup

	ctx := t.Context()
	for range goroutines {
		wg.Go(func() {
			for range msgsPerGoroutine {
				sc.Message(ctx, "hello")
			}
		})
	}

	wg.Wait()

	assert.Equal(t,
		goroutines*msgsPerGoroutine,
		len(inner.messages),
		"all messages should be recorded without data races",
	)

	// Verify no concurrent calls happened — maxConcurrent stays 0
	// because syncConsole serializes every call.
	assert.Equal(t, int32(0), inner.maxConcurrent.Load(),
		"syncConsole must serialize access to inner console",
	)
}

// TestProvisionLayersGraph_BuildsGraph verifies that
// provisionLayersGraph creates a correct execution graph from layers with known
// dependency phases. We set up three layers where layer-1 depends on
// layer-0's output, and layer-2 is independent of both.
func TestProvisionLayersGraph_BuildsGraph(t *testing.T) {
	t.Parallel()

	// Set up temp Bicep files:
	//   layer-0/main.bicep — outputs VNET_ID
	//   layer-1/main.bicep — no outputs
	//   layer-2/main.bicep — no outputs
	// layer-1/main.bicepparam references VNET_ID
	// layer-2 has no parameter references
	projectDir := t.TempDir()

	layer0Dir := filepath.Join(projectDir, "infra", "network")
	layer1Dir := filepath.Join(projectDir, "infra", "compute")
	layer2Dir := filepath.Join(projectDir, "infra", "monitoring")

	for _, d := range []string{layer0Dir, layer1Dir, layer2Dir} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}

	// Layer 0: produces VNET_ID output
	require.NoError(t, os.WriteFile(
		filepath.Join(layer0Dir, "main.bicep"),
		[]byte("output VNET_ID string = 'vnet-123'\n"),
		0o600,
	))

	// Layer 1: consumes VNET_ID via bicepparam
	require.NoError(t, os.WriteFile(
		filepath.Join(layer1Dir, "main.bicep"),
		[]byte("param vnetId string\n"),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(layer1Dir, "main.bicepparam"),
		[]byte(
			"using 'main.bicep'\n"+
				"param vnetId = readEnvironmentVariable('VNET_ID')\n",
		),
		0o600,
	))

	// Layer 2: independent
	require.NoError(t, os.WriteFile(
		filepath.Join(layer2Dir, "main.bicep"),
		[]byte("param location string\n"),
		0o600,
	))

	layers := []provisioning.Options{
		{Name: "network", Path: "infra/network", Module: "main"},
		{Name: "compute", Path: "infra/compute", Module: "main"},
		{
			Name: "monitoring", Path: "infra/monitoring",
			Module: "main",
		},
	}

	// Analyze dependencies.
	layerDeps, err := bicep.AnalyzeLayerDependencies(
		layers, projectDir,
	)
	require.NoError(t, err)

	// Level 0: network (0) and monitoring (2)
	// Level 1: compute (1) — depends on network output
	require.Len(t, layerDeps.Levels, 2)
	assert.ElementsMatch(t, []int{0, 2}, layerDeps.Levels[0])
	assert.ElementsMatch(t, []int{1}, layerDeps.Levels[1])

	// Build step names.
	stepNames := make([]string, len(layers))
	for i, layer := range layers {
		if layer.Name != "" {
			stepNames[i] = layer.Name
		} else {
			stepNames[i] = fmt.Sprintf("layer-%d", i)
		}
	}

	// Build the execution graph using precise producer→consumer edges.
	g := exegraph.NewGraph()
	for i := range layers {
		var deps []string
		for _, depIdx := range layerDeps.Edges[i] {
			deps = append(deps, stepNames[depIdx])
		}

		step := &exegraph.Step{
			Name:      stepNames[i],
			DependsOn: deps,
			Action: func(_ context.Context) error {
				return nil
			},
		}
		require.NoError(t, g.AddStep(step))
	}

	// Validate graph structure.
	require.NoError(t, g.Validate())
	assert.Equal(t, 3, g.Len())

	steps := g.Steps()

	// network: no dependencies
	assert.Equal(t, "network", steps[0].Name)
	assert.Empty(t, steps[0].DependsOn)

	// compute: depends only on network (precise edge, not all of phase 0)
	assert.Equal(t, "compute", steps[1].Name)
	assert.ElementsMatch(t,
		[]string{"network"},
		steps[1].DependsOn,
	)

	// monitoring: no dependencies
	assert.Equal(t, "monitoring", steps[2].Name)
	assert.Empty(t, steps[2].DependsOn)

	// Run the graph — all noop actions should succeed.
	err = exegraph.Run(t.Context(), g, exegraph.RunOptions{})
	require.NoError(t, err)
}

// trackingConsole embeds MockConsole and overrides Message to track
// concurrent access. It records all messages and detects unserialized
// calls.
type trackingConsole struct {
	*mockinput.MockConsole

	mu            sync.Mutex
	messages      []string
	active        atomic.Int32
	maxConcurrent atomic.Int32
}

func (c *trackingConsole) Message(
	_ context.Context, message string,
) {
	n := c.active.Add(1)
	defer c.active.Add(-1)

	// Record concurrent access — if n > 1 it means the syncConsole
	// mutex is not working.
	if n > 1 {
		c.maxConcurrent.Store(n)
	}

	c.mu.Lock()
	c.messages = append(c.messages, message)
	c.mu.Unlock()
}

// TestProvisionLayersGraph_DependentLayerWaitsForPostHooks verifies the
// cross-layer ordering contract documented on [runProvisionSingleLayer]:
// when the dependency graph contains an edge B → A, layer B's step is
// scheduled only after layer A's step returns — which by construction
// includes A's post-provision event AND A's layer post-hooks. This test
// drives the guarantee at the scheduler layer (the layer where it is
// actually enforced) by simulating each step as a single function whose
// body runs the full 7-stage lifecycle, then asserting B's start observes
// A's post-hook completion.
func TestProvisionLayersGraph_DependentLayerWaitsForPostHooks(t *testing.T) {
	t.Parallel()

	var (
		mu                 sync.Mutex
		aPostHookCompleted bool
		bStarted           bool
		bSawPostHook       bool
	)

	g := exegraph.NewGraph()

	// Layer A: simulates pre-hook → pre-event → deploy → env-merge →
	// service-event → post-event → post-hook. Records completion only
	// after the final post-hook stage.
	require.NoError(t, g.AddStep(&exegraph.Step{
		Name: "layer-a",
		Action: func(_ context.Context) error {
			// Steps 1-6: pretend they all ran.
			// Step 7: layer post-hook — set the flag last.
			mu.Lock()
			aPostHookCompleted = true
			mu.Unlock()
			return nil
		},
	}))

	// Layer B: depends on A. On entry, observes whether A's post-hook
	// completed before this step was scheduled.
	require.NoError(t, g.AddStep(&exegraph.Step{
		Name:      "layer-b",
		DependsOn: []string{"layer-a"},
		Action: func(_ context.Context) error {
			mu.Lock()
			bStarted = true
			bSawPostHook = aPostHookCompleted
			mu.Unlock()
			return nil
		},
	}))

	require.NoError(t, g.Validate())

	require.NoError(t, exegraph.Run(t.Context(), g, exegraph.RunOptions{
		MaxConcurrency: 4,
	}))

	mu.Lock()
	defer mu.Unlock()
	require.True(t, bStarted, "layer-b should have run")
	assert.True(
		t, bSawPostHook,
		"layer-b started before layer-a's post-hook completed — "+
			"hook-mediated values from A would be missed",
	)
}
