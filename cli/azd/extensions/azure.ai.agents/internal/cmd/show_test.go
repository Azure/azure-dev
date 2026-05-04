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

	result := &showResult{AgentVersionObject: version}
	err := printShowResultJSON(result)
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

	result := &showResult{
		AgentVersionObject: version,
		PlaygroundURL:      "https://ai.azure.com/nextgen/r/test/build/agents/test-agent/build?version=2",
		Endpoints: map[string]string{
			"Responses": "https://acct.services.ai.azure.com/api/projects/proj/agents/test-agent/endpoint/protocols/openai/responses?api-version=2025-11-15-preview",
		},
	}

	jsonBytes, err := json.MarshalIndent(result, "", "  ")
	require.NoError(t, err)

	var raw map[string]any
	err = json.Unmarshal(jsonBytes, &raw)
	require.NoError(t, err)

	assert.Equal(t, "agent.version", raw["object"])
	assert.Equal(t, "ver-456", raw["id"])
	assert.Equal(t, "test-agent", raw["name"])
	assert.Equal(t, "2", raw["version"])
	assert.Equal(t, "A test agent", raw["description"])
	assert.Equal(t, "active", raw["status"])
	assert.Equal(t, "guid-1234", raw["agent_guid"])
	metadata := raw["metadata"].(map[string]any)
	assert.Equal(t, "prod", metadata["env"])
	instanceIdentity := raw["instance_identity"].(map[string]any)
	assert.Equal(t, "inst-pid", instanceIdentity["principal_id"])
	assert.Equal(t, "inst-cid", instanceIdentity["client_id"])
	blueprint := raw["blueprint"].(map[string]any)
	assert.Equal(t, "bp-pid", blueprint["principal_id"])
	blueprintRef := raw["blueprint_reference"].(map[string]any)
	assert.Equal(t, "ManagedAgentIdentityBlueprint", blueprintRef["type"])
	assert.Equal(t, "test-agent-abc12", blueprintRef["blueprint_id"])

	// Verify new fields
	assert.Equal(t,
		"https://ai.azure.com/nextgen/r/test/build/agents/test-agent/build?version=2",
		raw["playground_url"],
	)
	endpoints := raw["agent_endpoints"].(map[string]any)
	assert.Contains(t, endpoints, "Responses")
}

func TestPrintAgentVersionJSON_NoLinks(t *testing.T) {
	version := &agent_api.AgentVersionObject{
		Object:  "agent.version",
		ID:      "ver-789",
		Name:    "my-agent",
		Version: "1",
	}

	result := &showResult{AgentVersionObject: version}
	jsonBytes, err := json.MarshalIndent(result, "", "  ")
	require.NoError(t, err)

	var raw map[string]any
	err = json.Unmarshal(jsonBytes, &raw)
	require.NoError(t, err)

	// playground_url and endpoints should be absent when empty
	_, hasPlayground := raw["playground_url"]
	assert.False(t, hasPlayground, "playground_url should be omitted when empty")
	_, hasEndpoints := raw["agent_endpoints"]
	assert.False(t, hasEndpoints, "agent_endpoints should be omitted when nil")
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

	result := &showResult{
		AgentVersionObject: version,
		PlaygroundURL:      "https://ai.azure.com/playground",
		Endpoints: map[string]string{
			"Responses": "https://example.com/responses",
		},
	}

	err := printShowResultTable(result)
	require.NoError(t, err)
}

func TestPrintAgentVersionTable_MinimalFields(t *testing.T) {
	version := &agent_api.AgentVersionObject{
		Object:  "agent.version",
		ID:      "ver-min",
		Name:    "minimal-agent",
		Version: "1",
	}

	result := &showResult{AgentVersionObject: version}
	err := printShowResultTable(result)
	require.NoError(t, err)
}
