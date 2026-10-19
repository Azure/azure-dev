// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package e2elive

import (
	"os"
	"path/filepath"
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

func TestEnableSampleMemorySnapshot(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		manifest string
		wantErr  string
	}{
		{"hosted", "services:\n  agent:\n    host: azure.ai.agent\n    kind: hosted\n    project: src\n", ""},
		{"referenced", "services:\n  agent:\n    host: azure.ai.agent\n    $ref: agent.yaml\n", ""},
		{"disabled", "services:\n  agent:\n    host: azure.ai.agent\n    enableMemorySnapshot: false\n", ""},
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

func TestValidateSnapshotAgentVersion(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		data    string
		wantErr string
	}{
		{"enabled active", `{"status":"active","definition":{"kind":"hosted","enable_memory_snapshot":true}}`, ""},
		{"missing opt-in", `{"status":"active","definition":{"kind":"hosted"}}`, "did not confirm"},
		{"disabled", `{"status":"active","definition":{"kind":"hosted","enable_memory_snapshot":false}}`, "did not confirm"},
		{"wrong kind", `{"status":"active","definition":{"kind":"prompt","enable_memory_snapshot":true}}`, "did not confirm"},
		{"creating", `{"status":"creating","definition":{"kind":"hosted","enable_memory_snapshot":true}}`, "not active"},
		{"failed", `{"status":"failed"}`, "not active"},
		{"invalid json", `{`, "parse deployed agent version"},
		{"wrong type", `{"status":"active","definition":{"kind":"hosted","enable_memory_snapshot":"true"}}`, "parse"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateSnapshotAgentVersion([]byte(testCase.data))
			if testCase.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, testCase.wantErr)
			}
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
}
