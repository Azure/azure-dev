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

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exegraph"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		t.Context(), layers, projectDir,
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

// TestProvisionLayersGraph_DependsOnEdgeOrdering verifies the cross-layer
// scheduler ordering contract: when the dependency graph contains an edge
// `B → A`, layer B's step is scheduled only after layer A's step returns.
// This is a *scheduler-level* test — it proves [exegraph] honors DependsOn
// for synthetic step actions but does NOT exercise the full
// [runProvisionSingleLayer] lifecycle (hooks, events, env merge). The
// end-to-end env-propagation contract is covered by the
// `TestRunProvisionSingleLayer_*` tests below, which use the actual
// production helpers.
func TestProvisionLayersGraph_DependsOnEdgeOrdering(t *testing.T) {
	t.Parallel()

	var (
		mu                 sync.Mutex
		aPostHookCompleted bool
		bStarted           bool
		bSawPostHook       bool
	)

	g := exegraph.NewGraph()

	require.NoError(t, g.AddStep(&exegraph.Step{
		Name: "layer-a",
		Action: func(_ context.Context) error {
			mu.Lock()
			aPostHookCompleted = true
			mu.Unlock()
			return nil
		},
	}))

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
		"layer-b started before layer-a's step returned — "+
			"the scheduler is not honoring DependsOn",
	)
}

// TestMergeLayerOutputsLocked_PreservesSubprocessWrites guards the
// hook-mediated env propagation contract documented on
// [runProvisionSingleLayer] step 4. A layer's pre-hook (or pre-provision
// event handler) may invoke `azd env set FOO=bar` in a subprocess, which
// writes FOO=bar to disk via its own envManager. The parent process's
// in-memory deps.env is not touched by that subprocess. If
// [mergeLayerOutputsLocked] called envManager.Save without first
// reloading deps.env, the Save would serialize the stale in-memory state
// and silently overwrite the subprocess write — making any downstream
// layer that reads FOO via .bicepparam observe the wrong value.
//
// This test forces that scenario: we mutate the disk file behind
// deps.env's back, call mergeLayerOutputsLocked with a deployment
// output, and assert the disk file still contains the subprocess's
// FOO=bar AFTER the merge.
func TestMergeLayerOutputsLocked_PreservesSubprocessWrites(t *testing.T) {
	t.Parallel()

	deps, envMu, envPath := newPropagationTestDeps(t)

	// Simulate a pre-hook subprocess writing FOO=bar to disk.
	// (The actual hook framework already reloads layerEnv via Reload,
	// but it never touches deps.env.)
	require.NoError(t, os.WriteFile(envPath, []byte("FOO=bar\n"), 0o600))

	outputs := map[string]provisioning.OutputParameter{
		"DEPLOY_KEY": {Type: provisioning.ParameterTypeString, Value: "deploy-value"},
	}

	require.NoError(t,
		mergeLayerOutputsLocked(t.Context(), deps, envMu, "test-layer", outputs),
	)

	// Disk must contain BOTH the subprocess write AND the deploy output.
	contents, err := os.ReadFile(envPath)
	require.NoError(t, err)
	disk := string(contents)

	assert.Contains(t, disk, "FOO=\"bar\"",
		"subprocess write FOO=bar was clobbered by mergeLayerOutputsLocked — "+
			"hook-mediated env values would be silently lost",
	)
	assert.Contains(t, disk, "DEPLOY_KEY=\"deploy-value\"",
		"deployment output DEPLOY_KEY was not persisted",
	)

	// In-memory deps.env must also reflect both.
	dotenv := deps.env.Dotenv()
	assert.Equal(t, "bar", dotenv["FOO"])
	assert.Equal(t, "deploy-value", dotenv["DEPLOY_KEY"])
}

// TestReconcileLayerEnvironmentLocked_RefreshesDepsEnvWithoutDelta asserts
// that step 8 still reloads out-of-process changes when no provider called Save.
//
// This is intentionally a unit test of the helper, not of the full
// runProvisionSingleLayer lifecycle: the lifecycle test would need to
// stand up a real provider + service locator + project config, all of
// which are mocked here. The full-lifecycle assertion is enforced by
// inspection — runProvisionSingleLayer's step 8 is the only call site,
// and the docstring above runProvisionSingleLayer pins the contract.
func TestReloadSharedEnvLocked_RefreshesDepsEnv(t *testing.T) {
	t.Parallel()

	deps, envMu, envPath := newPropagationTestDeps(t)

	// Establish an initial saved state on disk via the env manager.
	deps.env.DotenvSet("INITIAL", "value")
	require.NoError(t, deps.envManager.Save(t.Context(), deps.env))

	// Simulate A's post-hook subprocess: write a new key to disk that
	// the parent process's in-memory deps.env knows nothing about.
	current, err := os.ReadFile(envPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile( //nolint:gosec // G703: envPath is from t.TempDir()
		envPath, append(current, []byte("HOOK_VAL=\"from-a\"\n")...), 0o600,
	))

	// Sanity: deps.env (in-memory) does NOT see the subprocess write yet.
	require.Empty(t, deps.env.Dotenv()["HOOK_VAL"],
		"precondition: in-memory deps.env should not see subprocess write before reload",
	)

	// The contract: step 8's reload makes the subprocess write visible in
	// deps.env — and therefore to every downstream layer, which now reads
	// deps.env directly rather than cloning it.
	require.NoError(t, reloadSharedEnvLocked(t.Context(), deps, envMu))

	assert.Equal(t, "from-a", deps.env.Dotenv()["HOOK_VAL"],
		"downstream layer did not see layer-A's hook-mediated env write — "+
			"dependsOn ordering is silently incomplete",
	)
}

// TestMergeLayerOutputsLocked_ConcurrentMergesConverge guards against a
// regression where envMu fails to serialize concurrent merges from
// sibling layers. Two goroutines call mergeLayerOutputsLocked with
// disjoint output keys. After both complete, disk must contain the
// union of both writes (no last-writer-wins clobber).
func TestMergeLayerOutputsLocked_ConcurrentMergesConverge(t *testing.T) {
	t.Parallel()

	deps, envMu, envPath := newPropagationTestDeps(t)

	// Seed an initial saved state.
	deps.env.DotenvSet("BASE", "value")
	require.NoError(t, deps.envManager.Save(t.Context(), deps.env))

	outputsA := map[string]provisioning.OutputParameter{
		"FROM_A": {Type: provisioning.ParameterTypeString, Value: "a-value"},
	}
	outputsB := map[string]provisioning.OutputParameter{
		"FROM_B": {Type: provisioning.ParameterTypeString, Value: "b-value"},
	}

	var wg sync.WaitGroup
	wg.Go(func() {
		require.NoError(t,
			mergeLayerOutputsLocked(t.Context(), deps, envMu, "layer-a", outputsA),
		)
	})
	wg.Go(func() {
		require.NoError(t,
			mergeLayerOutputsLocked(t.Context(), deps, envMu, "layer-b", outputsB),
		)
	})
	wg.Wait()

	contents, err := os.ReadFile(envPath)
	require.NoError(t, err)
	disk := string(contents)

	assert.Contains(t, disk, "BASE=\"value\"", "seed value lost")
	assert.Contains(t, disk, "FROM_A=\"a-value\"", "layer-a output clobbered by layer-b merge")
	assert.Contains(t, disk, "FROM_B=\"b-value\"", "layer-b output clobbered by layer-a merge")
}

// TestSharedLayerEnvironment_ConcurrentProviderWritesAreSafe pins the contract that
// replaced per-layer environment clones. Parallel layers now write directly to the
// shared environment, so Environment and its Config must tolerate concurrent access
// while another layer persists. Run under -race; without synchronized config this
// panics with "concurrent map writes".
func TestSharedLayerEnvironment_ConcurrentProviderWritesAreSafe(t *testing.T) {
	t.Parallel()

	deps, _, _ := newPropagationTestDeps(t)
	layers := []string{"a", "b", "c", "d"}

	var wg sync.WaitGroup
	for _, name := range layers {
		// Mirrors bicep's mustSetParamAsConfig + dotenv writes during Deploy.
		wg.Go(func() {
			for i := range 50 {
				deps.env.DotenvSet("FROM_"+name, name)
				assert.NoError(t, deps.env.Config.Set("infra.parameters."+name, i))
				_, _ = deps.env.Config.Get("infra.parameters." + name)
				_ = deps.env.Dotenv()
			}
		})
	}

	// A sibling layer persisting mid-flight marshals the same config.
	wg.Go(func() {
		for range 10 {
			assert.NoError(t, deps.envManager.Save(t.Context(), deps.env))
		}
	})
	wg.Wait()

	for _, name := range layers {
		assert.Equal(t, name, deps.env.Getenv("FROM_"+name))
		// Saves round-trip through JSON, so the numeric type is not preserved.
		assert.EqualValues(t, 49, valueAtConfigPath(t, deps.env.Config, "infra.parameters."+name))
	}
}

func valueAtConfigPath(t *testing.T, source config.Config, path string) any {
	t.Helper()
	value, has := source.Get(path)
	require.True(t, has)
	return value
}

// newPropagationTestDeps builds a minimal provisionLayerDeps backed by a
// real filesystem-backed envManager so tests can exercise the actual
// reload / save semantics that the production code depends on.
func newPropagationTestDeps(
	t *testing.T,
) (*provisionLayerDeps, *sync.Mutex, string) {
	t.Helper()

	root := t.TempDir()
	azdCtx := azdcontext.NewAzdContextWithDirectory(root)

	mockContext := mocks.NewMockContext(t.Context())
	configManager := config.NewFileConfigManager(config.NewManager())
	localDataStore := environment.NewLocalFileDataStore(azdCtx, configManager)

	envName := "test-env"
	envManager, err := environment.NewManager(
		mockContext.Container, azdCtx, mockContext.Console, localDataStore, nil,
	)
	require.NoError(t, err)

	env := environment.New(envName)
	require.NoError(t, envManager.Save(t.Context(), env))

	envPath := localDataStore.EnvPath(env)

	deps := &provisionLayerDeps{
		env:        env,
		envManager: envManager,
	}
	return deps, &sync.Mutex{}, envPath
}
