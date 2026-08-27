// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
)

func TestLooksLikePromptAgentManifest(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "prompt agent",
			content: "kind: prompt\nname: my-agent\nmodel: gpt-4.1-mini\n",
			want:    true,
		},
		{
			name:    "prompt agent with harness",
			content: "kind: prompt\nname: my-agent\nmodel: gpt-4.1-mini\nharness:\n  type: github_copilot_preview\n",
			want:    true,
		},
		{
			name:    "kind casing is ignored",
			content: "kind: Prompt\nname: my-agent\n",
			want:    true,
		},
		{
			name:    "hosted container agent",
			content: "kind: container\nname: my-agent\nprotocols: []\n",
			want:    false,
		},
		{
			name:    "unified azure.yaml",
			content: "name: my-project\nservices:\n  agent:\n    host: azure.ai.agent\n",
			want:    false,
		},
		{
			name:    "not yaml",
			content: "\t\tnot: [valid",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksLikePromptAgentManifest([]byte(tt.content)); got != tt.want {
				t.Errorf("looksLikePromptAgentManifest = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLoadPromptAgentManifest(t *testing.T) {
	content := []byte(
		"kind: prompt\n" +
			"name: triage-agent\n" +
			"description: Triages incoming issues\n" +
			"model: gpt-4.1\n" +
			"harness:\n  type: github_copilot_preview\n  skills:\n    - name: summarize\n      version: \"2\"\n" +
			"instructions: You triage issues.\n" +
			"displayName: Triage Agent\n" +
			"metadata:\n  tags:\n    - Prompt Agent\n" +
			"skills:\n  - summarize\n" +
			"tools:\n  - type: code_interpreter\n",
	)

	manifest, err := loadPromptAgentManifest(content, "")
	if err != nil {
		t.Fatalf("loadPromptAgentManifest: %v", err)
	}
	if got := manifest.agentName(); got != "triage-agent" {
		t.Errorf("agentName = %q", got)
	}
	if got := manifest.model(); got != "gpt-4.1" {
		t.Errorf("model = %q", got)
	}
	if got := manifest.description(); got != "Triages incoming issues" {
		t.Errorf("description = %q", got)
	}
	if got := manifest.instructions(); got != "You triage issues." {
		t.Errorf("instructions = %q", got)
	}
	if got := manifest.definition.HarnessType(); got != agent_api.ManagedAgentHarnessGitHubCopilot {
		t.Errorf("harness = %q", got)
	}
	wantHarnessSkills := []agent_yaml.HarnessSkillRef{{Name: "summarize", Version: "2"}}
	if got := manifest.definition.Harness.Skills; !reflect.DeepEqual(got, wantHarnessSkills) {
		t.Errorf("harness skills = %+v, want %+v", got, wantHarnessSkills)
	}
	if len(manifest.definition.Skills) != 1 || len(manifest.definition.Tools) != 1 {
		t.Errorf("skills/tools were not carried through: %+v", manifest.definition)
	}
	// displayName and metadata are the catalog labels a hosted agent carries in
	// azure.yaml. They reach the same CreateAgentRequest fields for a prompt
	// agent, so the scaffold must not drop them.
	if manifest.definition.DisplayName == nil || *manifest.definition.DisplayName != "Triage Agent" {
		t.Errorf("displayName was not carried through: %+v", manifest.definition.DisplayName)
	}
	if manifest.definition.Metadata == nil {
		t.Fatal("metadata was not carried through")
	}
	if got := (*manifest.definition.Metadata)["tags"]; !reflect.DeepEqual(got, []any{"Prompt Agent"}) {
		t.Errorf("metadata tags = %+v", got)
	}
}

func TestLoadPromptAgentManifest_RejectsNonPromptKind(t *testing.T) {
	if _, err := loadPromptAgentManifest([]byte("kind: container\nname: a\n"), ""); err == nil {
		t.Fatal("expected an error for a non-prompt manifest kind")
	}
}

// Instructions are declared inline in the manifest, so the scaffold carries
// the authored prose through rather than the generic placeholder.
func TestPromptAgentManifest_InlineInstructions(t *testing.T) {
	authored := "You are a release notes summarizer."

	manifest, err := loadPromptAgentManifest(
		[]byte("kind: prompt\nname: a\nmodel: gpt-4.1-mini\ninstructions: "+authored+"\n"), t.TempDir(),
	)
	if err != nil {
		t.Fatalf("loadPromptAgentManifest: %v", err)
	}
	if got := manifest.instructions(); got != authored {
		t.Errorf("instructions = %q, want %q", got, authored)
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
