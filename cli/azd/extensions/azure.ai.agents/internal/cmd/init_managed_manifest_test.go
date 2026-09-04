// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
)

func TestWarnManifestOverridesKind(t *testing.T) {
	t.Parallel()

	flags := &initFlags{kind: "hosted"}
	var output bytes.Buffer
	warnManifestOverridesKind(&output, flags)

	require.Empty(t, flags.kind)
	require.Equal(t,
		"WARNING: Ignoring --kind because --manifest determines the agent type.\n",
		output.String(),
	)
}

func TestClassifyExplicitInitManifest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		content     string
		wantPrompt  bool
		wantUnified bool
	}{
		{
			name:       "bare prompt",
			content:    "kind: prompt\nname: prompt-agent\nmodel: gpt-4.1-mini\ninstructions: Help.\n",
			wantPrompt: true,
		},
		{
			name: "wrapped managed prompt",
			content: `name: managed-agent
template:
  kind: prompt
  name: managed-agent
  model: gpt-4.1-mini
  instructions: Help.
  harness:
    type: github_copilot_preview
`,
			wantPrompt: true,
		},
		{
			name: "hosted manifest",
			content: `name: hosted-agent
template:
  kind: hosted
  name: hosted-agent
  protocols:
    - protocol: responses
      version: 1
`,
		},
		{
			name: "unified azure yaml",
			content: `name: project
services:
  agent:
    host: azure.ai.agent
    kind: prompt
`,
			wantUnified: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			manifestPath := filepath.Join(dir, "manifest.yaml")
			require.NoError(t, os.WriteFile(manifestPath, []byte(test.content), 0o600))

			got, err := classifyExplicitInitManifest(
				t.Context(), nil, &initFlags{manifestPointer: manifestPath}, nil,
			)
			require.NoError(t, err)
			require.NotNil(t, got)
			require.Equal(t, test.wantPrompt, got.prompt != nil)
			require.Equal(t, test.wantUnified, got.unified)
		})
	}
}

// A nil manifest is the common case (no --manifest), so every accessor must be
// nil-safe rather than forcing the caller to branch.
func TestPromptAgentManifest_NilAccessors(t *testing.T) {
	var manifest *promptAgentManifest
	if manifest.agentName() != "" || manifest.model() != "" ||
		manifest.description() != "" || manifest.instructions() != "" {
		t.Error("nil manifest accessors should return empty strings")
	}
}

func TestPromptManifestDefinitionCopyPreservesAuthoredFields(t *testing.T) {
	t.Parallel()

	temperature := 0.0
	topP := 0.85
	manifest := &promptAgentManifest{definition: agent_yaml.PromptAgent{
		AgentDefinition: agent_yaml.AgentDefinition{Name: "authored-name"},
		Model:           "authored-model",
		Instructions:    "authored instructions",
		Temperature:     &temperature,
		TopP:            &topP,
		Text:            map[string]any{"format": map[string]any{"type": "json_object"}},
		Reasoning:       map[string]any{"effort": "low"},
	}}

	got := promptAgentForScaffold(
		manifest,
		"resolved-name",
		"resolved description",
		"resolved-model",
		"resolved instructions",
		agent_api.ManagedAgentHarnessGitHubCopilot,
	)

	require.Equal(t, "resolved-name", got.Name)
	require.Equal(t, "resolved-model", got.Model)
	require.Equal(t, "resolved instructions", got.Instructions)
	require.Equal(t, "resolved description", *got.Description)
	require.Equal(t, agent_api.ManagedAgentHarnessGitHubCopilot, got.Harness.Type)
	require.NotNil(t, got.Temperature)
	require.Equal(t, 0.0, *got.Temperature)
	require.NotNil(t, got.TopP)
	require.Equal(t, 0.85, *got.TopP)
	require.Equal(t, manifest.definition.Text, got.Text)
	require.Equal(t, manifest.definition.Reasoning, got.Reasoning)
}

// The manifest's `harness:` block is now resolved by the same
// resolveInitHarness used for --harness and the kind menu, so its precedence
// and validation are covered by TestResolveInitHarness.

// The two prompt flavors scaffold different projects into the same folder, so
// their suggested names must not collide.
func TestDefaultPromptAgentName(t *testing.T) {
	t.Parallel()

	plain := defaultPromptAgentName("")
	harnessed := defaultPromptAgentName(agent_api.ManagedAgentHarnessGitHubCopilot)

	require.NotEmpty(t, plain)
	require.NotEmpty(t, harnessed)
	require.NotEqual(t, plain, harnessed)
	// Whitespace is treated as "no harness" so a blank flag value cannot
	// silently pick the harnessed default.
	require.Equal(t, plain, defaultPromptAgentName("   "))
}

// The non-interactive guard runs before ensureProject writes anything, so these
// cases are what keeps a failed `--no-prompt` init from stranding a
// half-scaffolded project on disk.
func TestValidateManagedNoPromptInputs(t *testing.T) {
	promptManifest := func(name, model string) *promptAgentManifest {
		return &promptAgentManifest{
			definition: agent_yaml.PromptAgent{
				AgentDefinition: agent_yaml.AgentDefinition{
					Kind: agent_yaml.AgentKindPrompt,
					Name: name,
				},
				Model: model,
			},
		}
	}

	tests := []struct {
		name     string
		flags    initFlags
		manifest *promptAgentManifest
		wantErr  bool
	}{
		{
			name:  "interactive needs nothing up front",
			flags: initFlags{},
		},
		{
			name:    "no-prompt without name or model",
			flags:   initFlags{noPrompt: true},
			wantErr: true,
		},
		{
			name:    "no-prompt with name but no model",
			flags:   initFlags{noPrompt: true, agentName: "a"},
			wantErr: true,
		},
		{
			name:  "no-prompt with name and model",
			flags: initFlags{noPrompt: true, agentName: "a", model: "gpt-4.1-mini"},
		},
		{
			name:  "no-prompt with name and model deployment",
			flags: initFlags{noPrompt: true, agentName: "a", modelDeployment: "my-deployment"},
		},
		{
			name:     "no-prompt satisfied entirely by the manifest",
			flags:    initFlags{noPrompt: true},
			manifest: promptManifest("a", "gpt-4.1-mini"),
		},
		{
			name:     "no-prompt with a manifest missing a model",
			flags:    initFlags{noPrompt: true},
			manifest: promptManifest("a", ""),
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateManagedNoPromptInputs(&tt.flags, tt.manifest)
			if tt.wantErr && err == nil {
				t.Fatal("expected a validation error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validateManagedNoPromptInputs: %v", err)
			}
		})
	}
}
