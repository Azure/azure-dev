// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
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
	env := environment.NewWithValues("test", map[string]string{})

	result, err := AnalyzeLayerDependencies(layers, dir, env)
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
	// AZURE_LOCATION is in env so it does not create a dependency.
	env := environment.NewWithValues("test", map[string]string{
		"AZURE_LOCATION": "eastus",
	})

	result, err := AnalyzeLayerDependencies(layers, dir, env)
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
	env := environment.NewWithValues("test", map[string]string{})

	result, err := AnalyzeLayerDependencies(layers, dir, env)
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
	env := environment.NewWithValues("test", map[string]string{})

	result, err := AnalyzeLayerDependencies(layers, dir, env)
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
	env := environment.NewWithValues("test", map[string]string{})

	_, err := AnalyzeLayerDependencies(layers, dir, env)
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
	// VNET_ID is already in the env, so no dependency is created.
	env := environment.NewWithValues("test", map[string]string{
		"VNET_ID": "/subscriptions/.../vnet",
	})

	result, err := AnalyzeLayerDependencies(layers, dir, env)
	require.NoError(t, err)
	// Intra-graph producer/consumer edges are preserved even when the env var
	// pre-exists, to avoid consuming stale values when the producer's template
	// has changed. Unchanged producers go through the deployment-state-skipped
	// fast path, so the serial cost is near-zero.
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
	env := environment.NewWithValues("test", map[string]string{})

	result, err := AnalyzeLayerDependencies(layers, dir, env)
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
			got := extractParamEnvRefs(
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
	env := environment.NewWithValues("test", map[string]string{})

	_, err := AnalyzeLayerDependencies(layers, dir, env)
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
	env := environment.NewWithValues("test", map[string]string{})

	result, err := AnalyzeLayerDependencies(layers, dir, env)
	require.NoError(t, err)
	require.Equal(t, [][]int{{0}}, result.Levels)
}
