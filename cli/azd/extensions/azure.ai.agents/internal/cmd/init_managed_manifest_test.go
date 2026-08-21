// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

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
			content: "kind: prompt\nname: my-agent\nmodel: gpt-4.1-mini\nharness: github-copilot\n",
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
			"harness: github-copilot\n" +
			"instructions: You triage issues.\n" +
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
	if got := manifest.definition.Harness; got != agent_api.ManagedAgentHarnessGitHubCopilot {
		t.Errorf("harness = %q", got)
	}
	if len(manifest.definition.Skills) != 1 || len(manifest.definition.Tools) != 1 {
		t.Errorf("skills/tools were not carried through: %+v", manifest.definition)
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

func TestResolveManifestInitHarness(t *testing.T) {
	tests := []struct {
		name            string
		harnessFlag     string
		manifestHarness string
		want            string
		wantErr         bool
	}{
		{name: "manifest harness is honored", manifestHarness: "github-copilot", want: "github-copilot"},
		{name: "no harness anywhere means plain prompt agent"},
		{name: "harness flag wins", harnessFlag: "github-copilot", manifestHarness: "", want: "github-copilot"},
		{name: "harness none overrides manifest", harnessFlag: "none", manifestHarness: "github-copilot", want: ""},
		{name: "unknown flag value", harnessFlag: "bogus", wantErr: true},
		{name: "unknown manifest value", manifestHarness: "bogus", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveManifestInitHarness(tt.harnessFlag, tt.manifestHarness)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveManifestInitHarness: %v", err)
			}
			if got != tt.want {
				t.Errorf("harness = %q, want %q", got, tt.want)
			}
		})
	}
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
