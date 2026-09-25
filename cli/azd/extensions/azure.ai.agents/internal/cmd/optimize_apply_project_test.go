// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPersistPromptAgentCandidateConfigPreservesRawReferences(t *testing.T) {
	t.Parallel()

	const vaultRef = "vault://11111111-1111-1111-1111-111111111111/22222222-2222-2222-2222-222222222222"
	for _, fileName := range []string{"azure.yaml", "azure.yml"} {
		t.Run(fileName, func(t *testing.T) {
			t.Parallel()
			svc := newPromptCandidateTestService(t, false)
			server, path := newPromptCandidateTestServer(t, svc, false)
			expected := server.rawSections[svc.Name][path].AsMap()
			expected["description"] = vaultRef
			expected["name"] = "${AGENT_NAME}"
			expected["env"] = map[string]any{
				"VAULT_LITERAL": vaultRef,
				"TEMPLATE":      "${CUSTOM_SETTING}",
				"SERVER_VALUE":  "${{user.id}}",
			}
			root := t.TempDir()
			projectFile := writePromptCandidateTestProject(t, root, svc.Name, path, expected)
			if fileName != "azure.yaml" {
				require.NoError(t, os.Rename(projectFile, filepath.Join(root, fileName)))
			}
			client := newProjectRecorderClient(t, server)
			candidate := json.RawMessage(`{"model":"gpt-5","instructions":"Optimized instructions."}`)

			require.NoError(t, persistPromptAgentCandidateConfig(t.Context(), client, svc, root, candidate))

			expected["model"] = "gpt-5"
			expected["instructions"] = "Optimized instructions."
			server.mu.Lock()
			defer server.mu.Unlock()
			require.Empty(t, server.configSectionReads)
			require.Len(t, server.configSections, 1)
			require.Equal(t, svc.Name, server.configSections[0].ServiceName)
			require.Equal(t, path, server.configSections[0].Path)
			require.Equal(t, expected, server.configSections[0].Section.AsMap())
			require.Empty(t, server.configValues)
			require.Empty(t, server.unsetPaths)
		})
	}
}

func TestPersistPromptAgentCandidateConfigRejectsInvalidProjectShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		legacy  bool
		wantErr string
	}{
		{"empty file", "", false, "is missing or not a mapping"},
		{"services list", "services: []", false, "parsing project file"},
		{"service scalar", "services:\n  prompt-agent: invalid", false, "parsing project file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			projectFile := filepath.Join(root, "azure.yaml")
			require.NoError(t, os.WriteFile(projectFile, []byte(tt.content), 0600))
			svc := newPromptCandidateTestService(t, tt.legacy)
			server, _ := newPromptCandidateTestServer(t, svc, tt.legacy)
			client := newProjectRecorderClient(t, server)
			candidate := json.RawMessage(`{"model":"gpt-5","instructions":"Optimized instructions."}`)

			err := persistPromptAgentCandidateConfig(t.Context(), client, svc, root, candidate)

			require.ErrorContains(t, err, tt.wantErr)
			after, err := os.ReadFile(projectFile)
			require.NoError(t, err)
			require.Equal(t, tt.content, string(after))
			server.mu.Lock()
			defer server.mu.Unlock()
			require.Empty(t, server.configSectionReads)
			require.Empty(t, server.configSections)
			require.Empty(t, server.configValues)
			require.Empty(t, server.unsetPaths)
		})
	}
}
