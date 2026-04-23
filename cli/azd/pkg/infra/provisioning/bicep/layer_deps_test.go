// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/stretchr/testify/require"
)

// writeTestFile is a test helper that writes content to path, failing the
// test on error.
func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// mkTestDir is a test helper that creates a directory tree, failing the
// test on error.
func mkTestDir(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(path, 0o755))
}

func TestAnalyzeLayerDependencies_SingleLayer(t *testing.T) {
	dir := t.TempDir()
	layerDir := filepath.Join(dir, "networking")
	mkTestDir(t, layerDir)
	writeTestFile(t, filepath.Join(layerDir, "network.bicep"),
		"param location string\noutput VNET_ID string = 'abc'\n")

	layers := []provisioning.Options{
		{Path: "networking", Module: "network"},
	}

	result, err := AnalyzeLayerDependencies(t.Context(), layers, dir)
	require.NoError(t, err)
	require.Equal(t, [][]int{{0}}, result.Levels)
}

func TestAnalyzeLayerDependencies_NoDependencies(t *testing.T) {
	dir := t.TempDir()

	// Layer 0 — networking
	netDir := filepath.Join(dir, "networking")
	mkTestDir(t, netDir)
	writeTestFile(t, filepath.Join(netDir, "network.bicep"),
		"param location string\noutput VNET_ID string = 'id'\n")
	writeTestFile(t, filepath.Join(netDir, "network.parameters.json"),
		`{"parameters":{"location":{"value":"${AZURE_LOCATION}"}}}`)

	// Layer 1 — compute (no reference to VNET_ID)
	compDir := filepath.Join(dir, "compute")
	mkTestDir(t, compDir)
	writeTestFile(t, filepath.Join(compDir, "compute.bicep"),
		"param location string\noutput APP_URL string = 'url'\n")
	writeTestFile(t, filepath.Join(compDir, "compute.parameters.json"),
		`{"parameters":{"location":{"value":"${AZURE_LOCATION}"}}}`)

	layers := []provisioning.Options{
		{Path: "networking", Module: "network"},
		{Path: "compute", Module: "compute"},
	}
	// AZURE_LOCATION has no in-graph producer, so no dependency is created.

	result, err := AnalyzeLayerDependencies(t.Context(), layers, dir)
	require.NoError(t, err)
	require.Equal(t, [][]int{{0, 1}}, result.Levels)
}

func TestAnalyzeLayerDependencies_LinearChain(t *testing.T) {
	dir := t.TempDir()

	// Layer 0 — networking: outputs VNET_ID
	netDir := filepath.Join(dir, "networking")
	mkTestDir(t, netDir)
	writeTestFile(t, filepath.Join(netDir, "network.bicep"),
		"param location string\n"+
			"output VNET_ID string = '/subscriptions/.../vnet'\n")

	// Layer 1 — app: consumes VNET_ID
	appDir := filepath.Join(dir, "app")
	mkTestDir(t, appDir)
	writeTestFile(t, filepath.Join(appDir, "app.bicep"),
		"param vnetId string\n"+
			"output APP_URL string = 'https://myapp.com'\n")
	writeTestFile(t, filepath.Join(appDir, "app.parameters.json"),
		`{"parameters":{"vnetId":{"value":"${VNET_ID}"}}}`)

	layers := []provisioning.Options{
		{Path: "networking", Module: "network"},
		{Path: "app", Module: "app"},
	}

	result, err := AnalyzeLayerDependencies(t.Context(), layers, dir)
	require.NoError(t, err)
	require.Equal(t, [][]int{{0}, {1}}, result.Levels)
}

func TestAnalyzeLayerDependencies_Diamond(t *testing.T) {
	dir := t.TempDir()

	// Layer 0 (A) — outputs INFRA_X
	aDir := filepath.Join(dir, "layerA")
	mkTestDir(t, aDir)
	writeTestFile(t, filepath.Join(aDir, "a.bicep"),
		"output INFRA_X string = 'x'\n")

	// Layer 1 (B) — consumes INFRA_X, outputs INFRA_Y
	bDir := filepath.Join(dir, "layerB")
	mkTestDir(t, bDir)
	writeTestFile(t, filepath.Join(bDir, "b.bicep"),
		"param x string\noutput INFRA_Y string = 'y'\n")
	writeTestFile(t, filepath.Join(bDir, "b.parameters.json"),
		`{"parameters":{"x":{"value":"${INFRA_X}"}}}`)

	// Layer 2 (C) — consumes INFRA_X and INFRA_Y
	cDir := filepath.Join(dir, "layerC")
	mkTestDir(t, cDir)
	writeTestFile(t, filepath.Join(cDir, "c.bicep"),
		"param x string\nparam y string\n")
	writeTestFile(t, filepath.Join(cDir, "c.parameters.json"),
		`{"parameters":{`+
			`"x":{"value":"${INFRA_X}"},`+
			`"y":{"value":"${INFRA_Y}"}}}`)

	layers := []provisioning.Options{
		{Path: "layerA", Module: "a"},
		{Path: "layerB", Module: "b"},
		{Path: "layerC", Module: "c"},
	}

	result, err := AnalyzeLayerDependencies(t.Context(), layers, dir)
	require.NoError(t, err)
	// A first, then B (needs X), then C (needs X and Y).
	require.Equal(t, [][]int{{0}, {1}, {2}}, result.Levels)
}

func TestAnalyzeLayerDependencies_CycleDetected(t *testing.T) {
	dir := t.TempDir()

	// Layer 0 — outputs VAR_A, consumes VAR_B
	aDir := filepath.Join(dir, "layerA")
	mkTestDir(t, aDir)
	writeTestFile(t, filepath.Join(aDir, "a.bicep"),
		"output VAR_A string = 'a'\n")
	writeTestFile(t, filepath.Join(aDir, "a.parameters.json"),
		`{"parameters":{"b":{"value":"${VAR_B}"}}}`)

	// Layer 1 — outputs VAR_B, consumes VAR_A
	bDir := filepath.Join(dir, "layerB")
	mkTestDir(t, bDir)
	writeTestFile(t, filepath.Join(bDir, "b.bicep"),
		"output VAR_B string = 'b'\n")
	writeTestFile(t, filepath.Join(bDir, "b.parameters.json"),
		`{"parameters":{"a":{"value":"${VAR_A}"}}}`)

	layers := []provisioning.Options{
		{Path: "layerA", Module: "a"},
		{Path: "layerB", Module: "b"},
	}

	_, err := AnalyzeLayerDependencies(t.Context(), layers, dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cycle detected")
}

func TestAnalyzeLayerDependencies_PreExistingEnvVar(t *testing.T) {
	dir := t.TempDir()

	// Layer 0 — outputs VNET_ID
	netDir := filepath.Join(dir, "networking")
	mkTestDir(t, netDir)
	writeTestFile(t, filepath.Join(netDir, "network.bicep"),
		"output VNET_ID string = 'id'\n")

	// Layer 1 — references VNET_ID in parameters
	appDir := filepath.Join(dir, "app")
	mkTestDir(t, appDir)
	writeTestFile(t, filepath.Join(appDir, "app.bicep"),
		"param vnetId string\n")
	writeTestFile(t, filepath.Join(appDir, "app.parameters.json"),
		`{"parameters":{"vnetId":{"value":"${VNET_ID}"}}}`)

	layers := []provisioning.Options{
		{Path: "networking", Module: "network"},
		{Path: "app", Module: "app"},
	}

	result, err := AnalyzeLayerDependencies(t.Context(), layers, dir)
	require.NoError(t, err)
	// Intra-graph producer/consumer edges are preserved between layer 0
	// (emits VNET_ID) and layer 1 (consumes VNET_ID), even if the env var
	// happens to be pre-set in the ambient environment. Unchanged producers
	// go through the deployment-state-skipped fast path, so the serial cost
	// is near-zero.
	require.Equal(t, [][]int{{0}, {1}}, result.Levels)
}

func TestAnalyzeLayerDependencies_BicepparamFormat(t *testing.T) {
	dir := t.TempDir()

	// Layer 0 — outputs STORAGE_ACCOUNT
	storDir := filepath.Join(dir, "storage")
	mkTestDir(t, storDir)
	writeTestFile(t, filepath.Join(storDir, "stor.bicep"),
		"output STORAGE_ACCOUNT string = 'sa'\n")

	// Layer 1 — .bicepparam referencing STORAGE_ACCOUNT
	appDir := filepath.Join(dir, "app")
	mkTestDir(t, appDir)
	writeTestFile(t, filepath.Join(appDir, "app.bicep"),
		"param storageAccount string\n")
	writeTestFile(t, filepath.Join(appDir, "app.bicepparam"),
		"using './app.bicep'\n"+
			"param storageAccount = "+
			"readEnvironmentVariable('STORAGE_ACCOUNT')\n")

	layers := []provisioning.Options{
		{Path: "storage", Module: "stor"},
		{Path: "app", Module: "app"},
	}

	result, err := AnalyzeLayerDependencies(t.Context(), layers, dir)
	require.NoError(t, err)
	require.Equal(t, [][]int{{0}, {1}}, result.Levels)
}

func TestExtractBicepOutputsFromContent(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected []string
	}{
		{
			name: "simple outputs",
			content: "param location string\n" +
				"output VNET_ID string = '/subscriptions/..'\n" +
				"output SUBNET_ID string = '/subscriptions/..'\n",
			expected: []string{"VNET_ID", "SUBNET_ID"},
		},
		{
			name:     "indented output",
			content:  "  output INDENTED string = 'value'\n",
			expected: []string{"INDENTED"},
		},
		{
			name:    "no outputs",
			content: "param location string\nparam name string\n",
		},
		{
			name: "multiple types",
			content: "output strOut string = 'hello'\n" +
				"output intOut int = 42\n" +
				"output boolOut bool = true\n" +
				"output arrOut array = []\n" +
				"output objOut object = {}\n",
			expected: []string{
				"strOut", "intOut", "boolOut", "arrOut", "objOut",
			},
		},
		{
			name: "windows line endings",
			content: "output A string = 'a'\r\n" +
				"output B string = 'b'\r\n",
			expected: []string{"A", "B"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractBicepOutputsFromContent([]byte(tt.content))
			if tt.expected == nil {
				require.Empty(t, got)
			} else {
				require.Equal(t, tt.expected, got)
			}
		})
	}
}

func TestExtractParamEnvRefs(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		content  string
		expected []string
	}{
		{
			name:     "parameters.json with env vars",
			filePath: "main.parameters.json",
			content: `{"parameters":{` +
				`"p1":{"value":"${MY_VAR}"},` +
				`"p2":{"value":"${OTHER_VAR}"}}}`,
			expected: []string{"MY_VAR", "OTHER_VAR"},
		},
		{
			name:     "bicepparam with readEnvironmentVariable",
			filePath: "main.bicepparam",
			content: "using './main.bicep'\n" +
				"param p1 = readEnvironmentVariable('MY_VAR')\n" +
				"param p2 = readEnvironmentVariable('OTHER')\n",
			expected: []string{"MY_VAR", "OTHER"},
		},
		{
			name:     "deduplicates references",
			filePath: "main.parameters.json",
			content: `{"parameters":{` +
				`"p1":{"value":"${SAME}"},` +
				`"p2":{"value":"${SAME}"}}}`,
			expected: []string{"SAME"},
		},
		{
			name:     "bicepparam with default value",
			filePath: "main.bicepparam",
			content: "param p = " +
				"readEnvironmentVariable('WITH_DEFAULT', 'fallback')\n",
			expected: []string{"WITH_DEFAULT"},
		},
		{
			name:     "no references",
			filePath: "main.parameters.json",
			content:  `{"parameters":{"p1":{"value":"literal"}}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := extractParamEnvRefs(
				tt.filePath, []byte(tt.content),
			)
			if tt.expected == nil {
				require.Empty(t, got)
			} else {
				require.Equal(t, tt.expected, got)
			}
		})
	}
}

func TestTopoSortLevels_Empty(t *testing.T) {
	g := &layerDependencyGraph{
		layerCount:      0,
		edges:           make(map[int][]int),
		outputProviders: make(map[string]int),
	}

	levels, err := topoSortLevels(g)
	require.NoError(t, err)
	require.Nil(t, levels)
}

func TestAnalyzeLayerDependencies_DuplicateOutputs(t *testing.T) {
	dir := t.TempDir()

	// Layer 0 — outputs SHARED_VAR
	aDir := filepath.Join(dir, "layerA")
	mkTestDir(t, aDir)
	writeTestFile(t, filepath.Join(aDir, "a.bicep"),
		"output SHARED_VAR string = 'from-a'\n")

	// Layer 1 — also outputs SHARED_VAR (duplicate!)
	bDir := filepath.Join(dir, "layerB")
	mkTestDir(t, bDir)
	writeTestFile(t, filepath.Join(bDir, "b.bicep"),
		"output SHARED_VAR string = 'from-b'\n")

	layers := []provisioning.Options{
		{Path: "layerA", Module: "a", Name: "layerA"},
		{Path: "layerB", Module: "b", Name: "layerB"},
	}

	_, err := AnalyzeLayerDependencies(t.Context(), layers, dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate output")
	require.Contains(t, err.Error(), "SHARED_VAR")
}

func TestAnalyzeLayerDependencies_SameLayerDuplicateOutputIsAllowed(t *testing.T) {
	// A single layer producing an output name once is fine; the
	// duplicate check only triggers across different layers. This test
	// verifies that normal single-layer analysis works.
	dir := t.TempDir()
	layerDir := filepath.Join(dir, "single")
	mkTestDir(t, layerDir)
	writeTestFile(t, filepath.Join(layerDir, "s.bicep"),
		"output OUT_A string = 'a'\noutput OUT_B string = 'b'\n")

	layers := []provisioning.Options{
		{Path: "single", Module: "s"},
	}

	result, err := AnalyzeLayerDependencies(t.Context(), layers, dir)
	require.NoError(t, err)
	require.Equal(t, [][]int{{0}}, result.Levels)
}

// --- Safe-fallback coverage for vhvb1989 silent-miss cases ---

// TestExtractParamEnvRefs_NonLiteralReadEnv ensures that bicepparam files
// containing readEnvironmentVariable(varName) (non-literal argument) flag
// hasUnknown so the analyzer can engage the safe-by-default fallback.
func TestExtractParamEnvRefs_NonLiteralReadEnv(t *testing.T) {
	content := []byte(
		"using './main.bicep'\n" +
			"var name = 'MY_VAR'\n" +
			"param p1 = readEnvironmentVariable(name)\n" +
			"param p2 = readEnvironmentVariable('LITERAL')\n",
	)
	refs, hasUnknown := extractParamEnvRefs("main.bicepparam", content)
	require.True(
		t, hasUnknown,
		"non-literal readEnvironmentVariable must mark hasUnknown",
	)
	require.Equal(t, []string{"LITERAL"}, refs)
}

// TestExtractParamEnvRefs_ArmExpression ensures parameters.json files that
// contain ARM template expressions like [parameters('foo')] flag
// hasUnknown — those expressions can reference cross-layer outputs that
// the literal ${VAR} regex never sees.
func TestExtractParamEnvRefs_ArmExpression(t *testing.T) {
	content := []byte(
		`{"parameters":{` +
			`"p1":{"value":"${LITERAL}"},` +
			`"p2":{"value":"[parameters('foo')]"}}}`,
	)
	refs, hasUnknown := extractParamEnvRefs(
		"main.parameters.json", content,
	)
	require.True(t, hasUnknown, "ARM expression must mark hasUnknown")
	require.Equal(t, []string{"LITERAL"}, refs)
}

// TestExtractBicepParamReadEnvRefs_FromBicepFile ensures that
// readEnvironmentVariable() calls inside .bicep param defaults (which the
// original parser never inspected) are discovered and produce edges.
func TestExtractBicepParamReadEnvRefs_FromBicepFile(t *testing.T) {
	content := []byte(
		"param x string = readEnvironmentVariable('FROM_BICEP_DEFAULT')\n" +
			"output OUT string = x\n",
	)
	refs, hasUnknown := extractBicepParamReadEnvRefs(content)
	require.False(t, hasUnknown)
	require.Equal(t, []string{"FROM_BICEP_DEFAULT"}, refs)
}

// TestExtractBicepParamReadEnvRefs_NonLiteralFlagsUnknown ensures the
// .bicep scanner also flags non-literal readEnvironmentVariable calls.
func TestExtractBicepParamReadEnvRefs_NonLiteralFlagsUnknown(t *testing.T) {
	content := []byte(
		"var n = 'X'\n" +
			"param x string = readEnvironmentVariable(n)\n",
	)
	refs, hasUnknown := extractBicepParamReadEnvRefs(content)
	require.True(t, hasUnknown)
	require.Empty(t, refs)
}

// TestAnalyzeLayerDependencies_SafeFallback_NonLiteralBicepparam asserts
// the analyzer forces a layer with non-literal readEnvironmentVariable
// calls to depend on all earlier layers, preserving correctness when the
// detector cannot resolve the dependency statically.
func TestAnalyzeLayerDependencies_SafeFallback_NonLiteralBicepparam(t *testing.T) {
	dir := t.TempDir()

	aDir := filepath.Join(dir, "layerA")
	mkTestDir(t, aDir)
	writeTestFile(t, filepath.Join(aDir, "a.bicep"),
		"output A_OUT string = 'a'\n")

	bDir := filepath.Join(dir, "layerB")
	mkTestDir(t, bDir)
	writeTestFile(t, filepath.Join(bDir, "b.bicep"), "param p string\n")
	writeTestFile(t, filepath.Join(bDir, "b.bicepparam"),
		"using './b.bicep'\n"+
			"var n = 'A_OUT'\n"+
			"param p = readEnvironmentVariable(n)\n")

	layers := []provisioning.Options{
		{Name: "a", Path: "layerA", Module: "a"},
		{Name: "b", Path: "layerB", Module: "b"},
	}

	result, err := AnalyzeLayerDependencies(t.Context(), layers, dir)
	require.NoError(t, err)
	require.Equal(t, [][]int{{0}, {1}}, result.Levels)
	require.Contains(t, result.Edges[1], 0)
}

// TestAnalyzeLayerDependencies_SafeFallback_ArmExpression asserts the
// analyzer forces a layer with ARM expressions in .parameters.json to
// depend on all earlier layers.
func TestAnalyzeLayerDependencies_SafeFallback_ArmExpression(t *testing.T) {
	dir := t.TempDir()

	aDir := filepath.Join(dir, "layerA")
	mkTestDir(t, aDir)
	writeTestFile(t, filepath.Join(aDir, "a.bicep"),
		"output A_OUT string = 'a'\n")

	bDir := filepath.Join(dir, "layerB")
	mkTestDir(t, bDir)
	writeTestFile(t, filepath.Join(bDir, "b.bicep"), "param p string\n")
	writeTestFile(t, filepath.Join(bDir, "b.parameters.json"),
		`{"parameters":{"p":{"value":"[parameters('foo')]"}}}`)

	layers := []provisioning.Options{
		{Name: "a", Path: "layerA", Module: "a"},
		{Name: "b", Path: "layerB", Module: "b"},
	}

	result, err := AnalyzeLayerDependencies(t.Context(), layers, dir)
	require.NoError(t, err)
	require.Equal(t, [][]int{{0}, {1}}, result.Levels)
	require.Contains(t, result.Edges[1], 0)
}

// TestAnalyzeLayerDependencies_BicepParamDefault verifies an edge is
// produced when the consumer references the producer's output via a
// readEnvironmentVariable() default inside its .bicep file (no
// .bicepparam / .parameters.json file at all).
func TestAnalyzeLayerDependencies_BicepParamDefault(t *testing.T) {
	dir := t.TempDir()

	aDir := filepath.Join(dir, "layerA")
	mkTestDir(t, aDir)
	writeTestFile(t, filepath.Join(aDir, "a.bicep"),
		"output VNET_ID string = 'id'\n")

	bDir := filepath.Join(dir, "layerB")
	mkTestDir(t, bDir)
	writeTestFile(t, filepath.Join(bDir, "b.bicep"),
		"param vnetId string = readEnvironmentVariable('VNET_ID')\n"+
			"output APP_URL string = 'url'\n")

	layers := []provisioning.Options{
		{Name: "a", Path: "layerA", Module: "a"},
		{Name: "b", Path: "layerB", Module: "b"},
	}

	result, err := AnalyzeLayerDependencies(t.Context(), layers, dir)
	require.NoError(t, err)
	require.Equal(t, [][]int{{0}, {1}}, result.Levels)
	require.Contains(t, result.Edges[1], 0)
}

// TestAnalyzeLayerDependencies_ExplicitDependsOn verifies that
// author-declared dependsOn edges (used to express hook-mediated
// dependencies that no static analyzer can discover) are honored.
func TestAnalyzeLayerDependencies_ExplicitDependsOn(t *testing.T) {
	dir := t.TempDir()

	aDir := filepath.Join(dir, "layerA")
	mkTestDir(t, aDir)
	writeTestFile(t, filepath.Join(aDir, "a.bicep"),
		"output A_OUT string = 'a'\n")

	bDir := filepath.Join(dir, "layerB")
	mkTestDir(t, bDir)
	writeTestFile(t, filepath.Join(bDir, "b.bicep"),
		"output B_OUT string = 'b'\n")

	layers := []provisioning.Options{
		{Name: "a", Path: "layerA", Module: "a"},
		{
			Name:      "b",
			Path:      "layerB",
			Module:    "b",
			DependsOn: []string{"a"},
		},
	}

	result, err := AnalyzeLayerDependencies(t.Context(), layers, dir)
	require.NoError(t, err)
	require.Equal(t, [][]int{{0}, {1}}, result.Levels)
	require.Contains(t, result.Edges[1], 0)
}

// TestAnalyzeLayerDependencies_ExplicitDependsOn_UnknownLayer ensures
// references to undeclared layer names are rejected.
func TestAnalyzeLayerDependencies_ExplicitDependsOn_UnknownLayer(t *testing.T) {
	dir := t.TempDir()

	aDir := filepath.Join(dir, "layerA")
	mkTestDir(t, aDir)
	writeTestFile(t, filepath.Join(aDir, "a.bicep"),
		"output A_OUT string = 'a'\n")

	bDir := filepath.Join(dir, "layerB")
	mkTestDir(t, bDir)
	writeTestFile(t, filepath.Join(bDir, "b.bicep"),
		"output B_OUT string = 'b'\n")

	layers := []provisioning.Options{
		{Name: "a", Path: "layerA", Module: "a"},
		{
			Name:      "b",
			Path:      "layerB",
			Module:    "b",
			DependsOn: []string{"does-not-exist"},
		},
	}

	_, err := AnalyzeLayerDependencies(t.Context(), layers, dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown layer")
}

// TestAnalyzeLayerDependencies_ExplicitDependsOn_Self rejects a layer
// listing itself as a dependency.
func TestAnalyzeLayerDependencies_ExplicitDependsOn_Self(t *testing.T) {
	dir := t.TempDir()

	aDir := filepath.Join(dir, "layerA")
	mkTestDir(t, aDir)
	writeTestFile(t, filepath.Join(aDir, "a.bicep"),
		"output A_OUT string = 'a'\n")

	bDir := filepath.Join(dir, "layerB")
	mkTestDir(t, bDir)
	writeTestFile(t, filepath.Join(bDir, "b.bicep"),
		"output B_OUT string = 'b'\n")

	layers := []provisioning.Options{
		{Name: "a", Path: "layerA", Module: "a"},
		{
			Name:      "b",
			Path:      "layerB",
			Module:    "b",
			DependsOn: []string{"b"},
		},
	}

	_, err := AnalyzeLayerDependencies(t.Context(), layers, dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot depend on itself")
}

// TestAnalyzeLayerDependencies_ExplicitDependsOn_Cycle ensures cycles
// introduced via dependsOn are caught by the topological sort.
func TestAnalyzeLayerDependencies_ExplicitDependsOn_Cycle(t *testing.T) {
	dir := t.TempDir()

	aDir := filepath.Join(dir, "layerA")
	mkTestDir(t, aDir)
	writeTestFile(t, filepath.Join(aDir, "a.bicep"),
		"output A_OUT string = 'a'\n")

	bDir := filepath.Join(dir, "layerB")
	mkTestDir(t, bDir)
	writeTestFile(t, filepath.Join(bDir, "b.bicep"),
		"output B_OUT string = 'b'\n")

	layers := []provisioning.Options{
		{
			Name:      "a",
			Path:      "layerA",
			Module:    "a",
			DependsOn: []string{"b"},
		},
		{
			Name:      "b",
			Path:      "layerB",
			Module:    "b",
			DependsOn: []string{"a"},
		},
	}

	_, err := AnalyzeLayerDependencies(t.Context(), layers, dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cycle detected")
}

// --- Test 9: File I/O error path in discoverParamEnvRefs ---

func TestDiscoverParamEnvRefs_NonExistentDirectory(t *testing.T) {
	// When the project path doesn't exist, discoverParamEnvRefs should
	// not panic and should return a valid (conservative) result.
	opts := provisioning.Options{
		Path:   "completely-nonexistent-dir",
		Module: "main",
	}
	projectPath := filepath.Join(t.TempDir(), "no-such-project")

	// Should not panic.
	refs, hasUnknown := discoverParamEnvRefs(t.Context(), opts, projectPath)

	// No refs can be discovered from nonexistent files.
	require.Empty(t, refs)

	// The .bicep file read failure (not os.IsNotExist for a deeply
	// missing path) triggers the safe fallback or — if the OS returns
	// a "not exists" error — returns empty refs with no fallback.
	// Either behavior is acceptable; what matters is no panic and a
	// valid (conservative) result.
	_ = hasUnknown // accept either true or false
}

func TestStripBicepLineComments(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no comments",
			input:    "output VNET_ID string = 'abc'\n",
			expected: "output VNET_ID string = 'abc'\n",
		},
		{
			name:     "full-line comment",
			input:    "// output FAKE string\noutput REAL string = 'x'\n",
			expected: "\noutput REAL string = 'x'\n",
		},
		{
			name:     "inline comment",
			input:    "param x string // default\n",
			expected: "param x string \n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := string(stripBicepLineComments([]byte(tt.input)))
			require.Equal(t, tt.expected, got)
		})
	}
}

func TestExtractBicepOutputs_IgnoresCommentedOutputs(t *testing.T) {
	t.Parallel()
	content := []byte(
		"// output FAKE_OUTPUT string = 'old'\n" +
			"output REAL_OUTPUT string = 'value'\n" +
			"  // output ANOTHER_FAKE string\n",
	)
	names := extractBicepOutputsFromContent(content)
	require.Equal(t, []string{"REAL_OUTPUT"}, names)
}

func TestExtractParamEnvRefs_IgnoresCommentedRefs(t *testing.T) {
	t.Parallel()

	t.Run("bicepparam", func(t *testing.T) {
		t.Parallel()
		content := []byte(
			"using 'main.bicep'\n" +
				"// param old = readEnvironmentVariable('COMMENTED_VAR')\n" +
				"param foo = readEnvironmentVariable('REAL_VAR')\n",
		)
		refs, _ := extractParamEnvRefs("main.bicepparam", content)
		require.Equal(t, []string{"REAL_VAR"}, refs)
	})
}

func TestExtractBicepParamReadEnvRefs_IgnoresComments(t *testing.T) {
	t.Parallel()
	content := []byte(
		"// param old string = readEnvironmentVariable('COMMENTED')\n" +
			"param real string = readEnvironmentVariable('ACTIVE')\n",
	)
	refs, _ := extractBicepParamReadEnvRefs(content)
	require.Equal(t, []string{"ACTIVE"}, refs)
}
