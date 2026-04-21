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

func TestShowCommand_AcceptsPositionalArg(t *testing.T) {
	cmd := newShowCommand()
	err := cmd.Args(cmd, []string{"my-agent"})
	assert.NoError(t, err)
}

func TestShowCommand_AcceptsNoArgs(t *testing.T) {
	cmd := newShowCommand()
	err := cmd.Args(cmd, []string{})
	assert.NoError(t, err)
}

func TestShowCommand_RejectsMultipleArgs(t *testing.T) {
	cmd := newShowCommand()
	err := cmd.Args(cmd, []string{"svc1", "svc2"})
	assert.Error(t, err)
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

func TestPrintAgentVersionJSON(t *testing.T) {
	version := &agent_api.AgentVersionObject{
		Object:    "agent.version",
		ID:        "ver-123",
		Name:      "my-agent",
		Version:   "1",
		CreatedAt: 1735689600, // 2025-01-01T00:00:00Z
	}

	err := printAgentVersionJSON(version)
	require.NoError(t, err)
}

func TestPrintAgentVersionJSON_Format(t *testing.T) {
	desc := "A test agent"
	version := &agent_api.AgentVersionObject{
		Object:      "agent.version",
		ID:          "ver-456",
		Name:        "test-agent",
		Version:     "2",
		Description: &desc,
		Metadata:    map[string]string{"env": "prod"},
		CreatedAt:   1735689600,
		Status:      "active",
		InstanceIdentity: &agent_api.AgentIdentityInfo{
			PrincipalID: "inst-pid",
			ClientID:    "inst-cid",
		},
		Blueprint: &agent_api.BlueprintInfo{
			PrincipalID: "bp-pid",
			ClientID:    "bp-cid",
		},
		BlueprintReference: &agent_api.BlueprintReference{
			Type:        "ManagedAgentIdentityBlueprint",
			BlueprintID: "test-agent-abc12",
		},
		AgentGUID: "guid-1234",
	}

	jsonBytes, err := json.MarshalIndent(version, "", "  ")
	require.NoError(t, err)

	var result map[string]any
	err = json.Unmarshal(jsonBytes, &result)
	require.NoError(t, err)

	assert.Equal(t, "agent.version", result["object"])
	assert.Equal(t, "ver-456", result["id"])
	assert.Equal(t, "test-agent", result["name"])
	assert.Equal(t, "2", result["version"])
	assert.Equal(t, "A test agent", result["description"])
	assert.Equal(t, "active", result["status"])
	assert.Equal(t, "guid-1234", result["agent_guid"])
	metadata := result["metadata"].(map[string]any)
	assert.Equal(t, "prod", metadata["env"])
	instanceIdentity := result["instance_identity"].(map[string]any)
	assert.Equal(t, "inst-pid", instanceIdentity["principal_id"])
	assert.Equal(t, "inst-cid", instanceIdentity["client_id"])
	blueprint := result["blueprint"].(map[string]any)
	assert.Equal(t, "bp-pid", blueprint["principal_id"])
	blueprintRef := result["blueprint_reference"].(map[string]any)
	assert.Equal(t, "ManagedAgentIdentityBlueprint", blueprintRef["type"])
	assert.Equal(t, "test-agent-abc12", blueprintRef["blueprint_id"])
}

func TestPrintAgentVersionTable(t *testing.T) {
	desc := "A test agent"
	version := &agent_api.AgentVersionObject{
		Object:      "agent.version",
		ID:          "ver-789",
		Name:        "my-agent",
		Version:     "3",
		Description: &desc,
		Metadata:    map[string]string{"env": "staging"},
		CreatedAt:   1735689600,
		Status:      "active",
		AgentGUID:   "guid-5678",
		InstanceIdentity: &agent_api.AgentIdentityInfo{
			PrincipalID: "inst-pid",
			ClientID:    "inst-cid",
		},
		Blueprint: &agent_api.BlueprintInfo{
			PrincipalID: "bp-pid",
			ClientID:    "bp-cid",
		},
		BlueprintReference: &agent_api.BlueprintReference{
			Type:        "ManagedAgentIdentityBlueprint",
			BlueprintID: "my-agent-abc12",
		},
	}

	err := printAgentVersionTable(version)
	require.NoError(t, err)
}

func TestPrintAgentVersionTable_MinimalFields(t *testing.T) {
	version := &agent_api.AgentVersionObject{
		Object:  "agent.version",
		ID:      "ver-min",
		Name:    "minimal-agent",
		Version: "1",
	}

	err := printAgentVersionTable(version)
	require.NoError(t, err)
}
