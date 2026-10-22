// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package e2elive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestResponseHasExpectedAnswer(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		text string
		want bool
	}{
		{"plain four digit", "The answer is 4.", true},
		{"bare four", "4", true},
		{"equation", "2+2=4", true},
		{"spelled word", "It is four.", true},
		{"spelled upper", "FOUR", true},
		{"parenthesized", "(4)", true},
		{"trailing period mid-sentence", "the value 4. is final", true},
		{"model name", "gpt-4o-mini", false},
		{"version", "4.1", false},
		{"decimal four", "4.0", false}, // intentional: see responseHasExpectedAnswer doc
		{"status code", "404", false},
		{"price", "$40", false},
		{"ratio", "24/7", false},
		{"fourteen", "fourteen apples", false},
		{"no answer", "I am not sure", false},
		{"empty", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := responseHasExpectedAnswer(tc.text); got != tc.want {
				t.Errorf("responseHasExpectedAnswer(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestAgentResponseRegion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		out  string
		want bool // responseHasExpectedAnswer over the sliced region
	}{
		{
			"answer scoped between markers",
			"using model gpt-4o-mini\n[agent] The answer is 4.\nServer responded in 2s (first byte: 1s)\n",
			true,
		},
		{
			"answer scoped before client elapsed",
			"[agent] The answer is 4.\nClient elapsed: 2.000s\n",
			true,
		},
		{
			"latency measurements are not part of the answer",
			"[agent] I am not sure.\nClient elapsed: 1.000s\n" +
				"Platform latency (warm): response headers 4 ms\n  preprocess 0 ms | container 4 ms\n",
			false,
		},
		{
			"stray digits outside region rejected",
			"gpt-4o-mini deployed (404 cached)\n[agent] I am not sure.\nServer responded in 4.0s\n",
			false,
		},
		{
			"standalone 4 before agent line excluded by region",
			"completed step 4\n[agent] I don't know.\nServer responded in 1s\n",
			false,
		},
		{
			"missing footer falls back to full text",
			"using gpt-4o-mini\n[agent] four",
			true,
		},
		{
			"no agent line falls back to full text",
			"the answer is four",
			true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := responseHasExpectedAnswer(agentResponseRegion(tc.out)); got != tc.want {
				t.Errorf("region(%q) -> %v, want %v", tc.out, got, tc.want)
			}
		})
	}
}

func TestUpdateSnapshotSampleManifest(t *testing.T) {
	original := []byte("services:\n  agent:\n    host: azure.ai.agent\n    kind: hosted\n    codeConfiguration:\n      runtime: python_3.13\n")
	t.Run("updates within project root", func(t *testing.T) {
		root, err := os.OpenRoot(t.TempDir())
		require.NoError(t, err)
		defer root.Close()
		require.NoError(t, root.WriteFile("azure.yaml", original, 0o600))
		require.NoError(t, updateSnapshotSampleManifest(root))
		data, err := root.ReadFile("azure.yaml")
		require.NoError(t, err)
		require.Contains(t, string(data), "enableMemorySnapshot: true")
	})
	t.Run("rejects escaping manifest symlink", func(t *testing.T) {
		outside := filepath.Join(t.TempDir(), "outside.yaml")
		require.NoError(t, os.WriteFile(outside, original, 0o600))
		projectDir := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(projectDir, "azure.yaml")); err != nil {
			t.Skipf("symlink creation is unavailable: %v", err)
		}
		root, err := os.OpenRoot(projectDir)
		require.NoError(t, err)
		defer root.Close()
		require.Error(t, updateSnapshotSampleManifest(root))
		data, err := os.ReadFile(outside)
		require.NoError(t, err)
		require.Equal(t, original, data)
	})
}

func TestEnableSampleMemorySnapshot(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		manifest string
		wantErr  string
	}{
		{"code", "services:\n  agent:\n    host: azure.ai.agent\n    kind: hosted\n    codeConfiguration:\n      runtime: python_3.13\n      entryPoint: main.py\n", ""},
		{"disabled", "services:\n  agent:\n    host: azure.ai.agent\n    enableMemorySnapshot: false\n    codeConfiguration:\n      runtime: python_3.13\n", ""},
		{"missing code", "services:\n  agent:\n    host: azure.ai.agent\n    kind: hosted\n", "requires codeConfiguration"},
		{"invalid code", "services:\n  agent:\n    host: azure.ai.agent\n    codeConfiguration: true\n", "requires codeConfiguration"},
		{"prompt", "services:\n  agent:\n    host: azure.ai.agent\n    kind: prompt\n", "hosted agent"},
		{"no services", "name: sample\n", "must contain services"},
		{"no agent", "services:\n  project:\n    host: azure.ai.project\n", "no azure.ai.agent"},
		{"multiple agents", "services:\n  first:\n    host: azure.ai.agent\n  second:\n    host: azure.ai.agent\n", "exactly one"},
		{"invalid yaml", "services: [", "parse sample manifest"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			data, err := enableSampleMemorySnapshot([]byte(testCase.manifest))
			if testCase.wantErr != "" {
				require.ErrorContains(t, err, testCase.wantErr)
				return
			}
			require.NoError(t, err)
			var before, after struct {
				Services map[string]map[string]any `yaml:"services"`
			}
			require.NoError(t, yaml.Unmarshal([]byte(testCase.manifest), &before))
			require.NoError(t, yaml.Unmarshal(data, &after))
			before.Services["agent"]["enableMemorySnapshot"] = true
			require.Equal(t, before, after)
		})
	}
}

func TestSnapshotLivePipelineConfiguration(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "..", "..", "eng", "pipelines", "ext-azure-ai-agents-live.yml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var pipeline struct {
		Parameters []struct {
			Name    string `yaml:"name"`
			Default any    `yaml:"default"`
		} `yaml:"parameters"`
		Extends struct {
			Parameters struct {
				Stages []struct {
					Jobs []struct {
						Steps []map[string]yaml.Node `yaml:"steps"`
					} `yaml:"jobs"`
				} `yaml:"stages"`
			} `yaml:"parameters"`
		} `yaml:"extends"`
	}
	require.NoError(t, yaml.Unmarshal(data, &pipeline))
	defaults := map[string]any{}
	for _, parameter := range pipeline.Parameters {
		defaults[parameter.Name] = parameter.Default
	}
	require.Equal(t, false, defaults["memorySnapshot"])
	require.Equal(t, "", defaults["templateURL"])
	require.Equal(t, "eastus2", defaults["location"])
	require.Equal(t, "both", defaults["deployModes"])
	require.NotContains(t, defaults, "snapshotImage")
	require.Equal(t, "", defaults["subscriptionId"])
	var subscriptionBoundSteps int
	for _, stage := range pipeline.Extends.Parameters.Stages {
		for _, job := range stage.Jobs {
			for _, step := range job.Steps {
				displayName := step["displayName"].Value
				if displayName != "Run Tier 2 live golden path" && displayName != "Cleanup leaked Azure resources" {
					continue
				}
				var inputs struct {
					InlineScript string `yaml:"inlineScript"`
				}
				var env map[string]yaml.Node
				inputsNode, envNode := step["inputs"], step["env"]
				require.NoError(t, inputsNode.Decode(&inputs))
				require.NoError(t, envNode.Decode(&env))
				require.Equal(t, "${{ parameters.subscriptionId }}", env["E2E_TARGET_SUBSCRIPTION"].Value)
				selectSubscription := strings.Index(inputs.InlineScript, `az account set --subscription "$E2E_TARGET_SUBSCRIPTION"`)
				require.NotEqual(t, -1, selectSubscription)
				if displayName == "Run Tier 2 live golden path" {
					require.Contains(t, inputs.InlineScript, "requires an explicit preview-approved subscriptionId")
					require.Contains(t, inputs.InlineScript, "does not match the requested subscriptionId")
					readSubscription := strings.Index(inputs.InlineScript, `E2E_SUBSCRIPTION="$(az account show`)
					require.Greater(t, readSubscription, selectSubscription)
				} else {
					require.Contains(t, inputs.InlineScript, `export AZURE_SUBSCRIPTION_ID`)
					require.Contains(t, inputs.InlineScript, `az group update --subscription "$AZURE_SUBSCRIPTION_ID"`)
				}
				subscriptionBoundSteps++
			}
		}
	}
	require.Equal(t, 2, subscriptionBoundSteps)
}
