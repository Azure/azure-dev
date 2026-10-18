// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"

	"azureaiagent/internal/pkg/agents/agent_api"
)

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
	tests := []struct {
		name    string
		flags   initFlags
		wantErr bool
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
			name: "no-prompt with name, existing project, and model deployment",
			flags: initFlags{
				noPrompt: true, agentName: "a", modelDeployment: "my-deployment", projectResourceId: "/project",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateManagedNoPromptInputs(&tt.flags)
			if tt.wantErr && err == nil {
				t.Fatal("expected a validation error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validateManagedNoPromptInputs: %v", err)
			}
		})
	}
}

func TestPromptProjectLayoutUsesParentProject(t *testing.T) {
	projectRoot := t.TempDir()
	cwd := filepath.Join(projectRoot, "nested")
	require.NoError(t, os.MkdirAll(cwd, 0o750))

	existing, target, rel, source, err := promptProjectLayout(
		&azdext.ProjectConfig{Path: projectRoot}, nil, cwd, "assistant",
	)
	require.NoError(t, err)
	require.True(t, existing)
	require.Equal(t, ".", target)
	require.Equal(t, "nested/assistant", rel)
	require.Equal(t, filepath.Join(projectRoot, "nested", "assistant"), source)
}
