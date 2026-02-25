// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"testing"
	"text/tabwriter"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusCommand_RequiredFlags(t *testing.T) {
	cmd := newStatusCommand()

	// Execute with no flags should fail (missing required flags)
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "name")
}

func TestStatusCommand_MissingVersionFlag(t *testing.T) {
	cmd := newStatusCommand()

	cmd.SetArgs([]string{"--name", "test-agent"})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "version")
}

func TestPrintStatusJSON(t *testing.T) {
	container := &agent_api.AgentContainerObject{
		Object: "container",
		Status: agent_api.AgentContainerStatusRunning,
	}

	// Should not error
	err := printStatusJSON(container)
	require.NoError(t, err)
}

func TestPrintStatusTable(t *testing.T) {
	container := &agent_api.AgentContainerObject{
		Object:    "container",
		Status:    agent_api.AgentContainerStatusRunning,
		CreatedAt: "2025-01-01T00:00:00Z",
		UpdatedAt: "2025-01-01T01:00:00Z",
	}

	// Should not error
	err := printStatusTable(container)
	require.NoError(t, err)
}

func TestPrintStatusJSON_Format(t *testing.T) {
	container := &agent_api.AgentContainerObject{
		Object: "container",
		Status: agent_api.AgentContainerStatusRunning,
	}

	jsonBytes, err := json.MarshalIndent(container, "", "  ")
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(jsonBytes, &result)
	require.NoError(t, err)

	assert.Equal(t, "Running", result["status"])
	assert.Equal(t, "container", result["object"])
}

func TestPrintField(t *testing.T) {
	tests := []struct {
		name     string
		data     map[string]interface{}
		key      string
		label    string
		expected string
	}{
		{
			name:     "existing field",
			data:     map[string]interface{}{"status": "Running"},
			key:      "status",
			label:    "Status",
			expected: "Status Running\n",
		},
		{
			name:     "missing field",
			data:     map[string]interface{}{},
			key:      "status",
			label:    "Status",
			expected: "",
		},
		{
			name:     "nil field",
			data:     map[string]interface{}{"status": nil},
			key:      "status",
			label:    "Status",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := tabwriter.NewWriter(&buf, 0, 0, 1, ' ', 0)
			printField(w, tt.data, tt.key, tt.label)
			w.Flush()
			assert.Equal(t, tt.expected, buf.String())
		})
	}
}

func TestBuildAgentEndpoint(t *testing.T) {
	endpoint := buildAgentEndpoint("myAccount", "myProject")
	assert.Equal(t, "https://myAccount.services.ai.azure.com/api/projects/myProject", endpoint)
}

func TestResolveAgentEndpoint_PartialFlags(t *testing.T) {
	// Providing only one of account-name/project-name should error
	_, err := resolveAgentEndpoint(t.Context(), "myAccount", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "both --account-name and --project-name must be provided together")

	_, err = resolveAgentEndpoint(t.Context(), "", "myProject")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "both --account-name and --project-name must be provided together")
}

func TestResolveAgentEndpoint_BothFlags(t *testing.T) {
	endpoint, err := resolveAgentEndpoint(t.Context(), "myAccount", "myProject")
	require.NoError(t, err)
	assert.Equal(t, "https://myAccount.services.ai.azure.com/api/projects/myProject", endpoint)
}

func TestNewAgentContext_WithFlags(t *testing.T) {
	ac, err := newAgentContext(t.Context(), "myAccount", "myProject", "my-agent", "1")
	require.NoError(t, err)
	assert.Equal(t, "https://myAccount.services.ai.azure.com/api/projects/myProject", ac.ProjectEndpoint)
	assert.Equal(t, "my-agent", ac.Name)
	assert.Equal(t, "1", ac.Version)
}

func TestNewAgentContext_PartialFlags(t *testing.T) {
	_, err := newAgentContext(t.Context(), "myAccount", "", "my-agent", "1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "both --account-name and --project-name must be provided together")
}

func TestStatusCommand_DefaultOutputFlag(t *testing.T) {
	cmd := newStatusCommand()

	output, _ := cmd.Flags().GetString("output")
	assert.Equal(t, "json", output)
}
