// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShowCommand_RequiredFlags(t *testing.T) {
	cmd := newShowCommand()

	// Execute with no flags should fail (missing required flags)
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "name")
}

func TestShowCommand_MissingVersionFlag(t *testing.T) {
	cmd := newShowCommand()

	cmd.SetArgs([]string{"--name", "test-agent"})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "version")
}

func TestPrintStatusJSON(t *testing.T) {
	container := &agent_api.AgentContainerObject{
		Object: "agent.container",
		ID:     "test-agent-1",
		Status: agent_api.AgentContainerStatusRunning,
	}

	// Should not error
	err := printStatusJSON(container)
	require.NoError(t, err)
}

func TestPrintStatusTable(t *testing.T) {
	minReplicas := int32(1)
	maxReplicas := int32(3)
	container := &agent_api.AgentContainerObject{
		Object:      "agent.container",
		ID:          "test-agent-1",
		Status:      agent_api.AgentContainerStatusRunning,
		MinReplicas: &minReplicas,
		MaxReplicas: &maxReplicas,
		CreatedAt:   "2025-01-01T00:00:00Z",
		UpdatedAt:   "2025-01-01T01:00:00Z",
		Container: &agent_api.AgentContainerDetails{
			HealthState:       "Healthy",
			ProvisioningState: "Succeeded",
			State:             "Running",
			UpdatedOn:         "2025-01-01T01:00:00Z",
			Replicas: []agent_api.AgentContainerReplicaState{
				{Name: "replica-1", State: "Running", ContainerState: "Running"},
			},
		},
	}

	// Should not error
	err := printStatusTable(container)
	require.NoError(t, err)
}

func TestPrintStatusTable_NoContainer(t *testing.T) {
	container := &agent_api.AgentContainerObject{
		Object:    "agent.container",
		ID:        "test-agent-1",
		Status:    agent_api.AgentContainerStatusRunning,
		CreatedAt: "2025-01-01T00:00:00Z",
		UpdatedAt: "2025-01-01T01:00:00Z",
	}

	// Should not error even without nested container details
	err := printStatusTable(container)
	require.NoError(t, err)
}

func TestPrintStatusJSON_Format(t *testing.T) {
	container := &agent_api.AgentContainerObject{
		Object: "agent.container",
		ID:     "test-agent-1",
		Status: agent_api.AgentContainerStatusRunning,
	}

	jsonBytes, err := json.MarshalIndent(container, "", "  ")
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(jsonBytes, &result)
	require.NoError(t, err)

	assert.Equal(t, "Running", result["status"])
	assert.Equal(t, "agent.container", result["object"])
	assert.Equal(t, "test-agent-1", result["id"])
}

func TestPrintStatusJSON_WithContainerDetails(t *testing.T) {
	container := &agent_api.AgentContainerObject{
		Object: "agent.container",
		ID:     "basic-sample-1",
		Status: agent_api.AgentContainerStatusFailed,
		Container: &agent_api.AgentContainerDetails{
			HealthState:       "Unhealthy",
			ProvisioningState: "Succeeded",
			State:             "ActivationFailed",
			Replicas: []agent_api.AgentContainerReplicaState{
				{Name: "replica-abc", State: "NotRunning", ContainerState: "Waiting"},
			},
		},
	}

	jsonBytes, err := json.MarshalIndent(container, "", "  ")
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(jsonBytes, &result)
	require.NoError(t, err)

	assert.Equal(t, "Failed", result["status"])
	containerMap := result["container"].(map[string]interface{})
	assert.Equal(t, "Unhealthy", containerMap["health_state"])
	assert.Equal(t, "ActivationFailed", containerMap["state"])
	replicas := containerMap["replicas"].([]interface{})
	assert.Len(t, replicas, 1)
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

func TestShowCommand_DefaultOutputFlag(t *testing.T) {
	cmd := newShowCommand()

	output, _ := cmd.Flags().GetString("output")
	assert.Equal(t, "json", output)
}
