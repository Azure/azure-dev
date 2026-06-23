// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"os"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrintEndpointTable_FullConfig(t *testing.T) {
	pct80 := int32(80)
	pct20 := int32(20)
	ver := "2.0"
	agent := &agent_api.AgentObject{
		Name: "patch-poc-resp",
		AgentEndpoint: &agent_api.AgentEndpoint{
			Protocols: []agent_api.AgentEndpointProtocol{"responses", "a2a", "mcp"},
			VersionSelector: &agent_api.VersionSelector{
				VersionSelectionRules: []agent_api.VersionSelectionRule{
					{Type: "FixedRatio", AgentVersion: "@latest", TrafficPercentage: &pct80},
					{Type: "FixedRatio", AgentVersion: "1", TrafficPercentage: &pct20},
				},
			},
			AuthorizationSchemes: []agent_api.AgentEndpointAuthorizationScheme{
				{
					Type:               "Entra",
					IsolationKeySource: &agent_api.IsolationKeySource{Kind: "Header"},
				},
			},
		},
		AgentCard: &agent_api.AgentCard{
			Description: "Updated echo agent",
			Version:     &ver,
			Skills: []agent_api.AgentCardSkill{
				{ID: "echo", Name: "Echo", Description: "Echoes user input"},
				{ID: "stream", Name: "Stream Echo", Description: "Streaming echo"},
			},
		},
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := printEndpointTable(agent)
	require.NoError(t, err)

	_ = w.Close() //nolint:gosec
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	assert.Contains(t, output, "patch-poc-resp")
	assert.Contains(t, output, "responses, a2a, mcp")
	assert.Contains(t, output, "@latest")
	assert.Contains(t, output, "80%")
	assert.Contains(t, output, "20%")
	assert.Contains(t, output, "Header")
	assert.Contains(t, output, "Updated echo agent")
	assert.Contains(t, output, "Echo")
	assert.Contains(t, output, "Stream Echo")

	t.Logf("=== endpoint show output ===\n%s", output)
}

func TestPrintEndpointTable_NilEndpoint(t *testing.T) {
	agent := &agent_api.AgentObject{
		Name: "empty-agent",
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := printEndpointTable(agent)
	require.NoError(t, err)

	_ = w.Close() //nolint:gosec
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	assert.Contains(t, output, "empty-agent")
	assert.Contains(t, output, "(not configured)")

	t.Logf("=== endpoint show output (nil) ===\n%s", output)
}

func TestPrintEndpointJSON(t *testing.T) {
	pct100 := int32(100)
	agent := &agent_api.AgentObject{
		Name: "json-test-agent",
		AgentEndpoint: &agent_api.AgentEndpoint{
			Protocols: []agent_api.AgentEndpointProtocol{"responses"},
			VersionSelector: &agent_api.VersionSelector{
				VersionSelectionRules: []agent_api.VersionSelectionRule{
					{Type: "FixedRatio", AgentVersion: "@latest", TrafficPercentage: &pct100},
				},
			},
			AuthorizationSchemes: []agent_api.AgentEndpointAuthorizationScheme{
				{Type: "Entra", IsolationKeySource: &agent_api.IsolationKeySource{Kind: "Entra"}},
			},
		},
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := printEndpointJSON(agent)
	require.NoError(t, err)

	_ = w.Close() //nolint:gosec
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	assert.Contains(t, output, `"name": "json-test-agent"`)
	assert.Contains(t, output, `"protocols"`)
	assert.Contains(t, output, `"responses"`)

	t.Logf("=== endpoint show --output json ===\n%s", output)
}

func TestPrintEndpointTable_ProtocolConfiguration(t *testing.T) {
	agent := &agent_api.AgentObject{
		Name: "proto-config-agent",
		AgentEndpoint: &agent_api.AgentEndpoint{
			ProtocolConfiguration: &agent_api.ProtocolConfiguration{
				Responses:   &agent_api.ResponsesProtocolConfiguration{},
				A2A:         &agent_api.A2AProtocolConfiguration{},
				Invocations: &agent_api.InvocationsProtocolConfiguration{},
			},
		},
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := printEndpointTable(agent)
	require.NoError(t, err)

	_ = w.Close() //nolint:gosec
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	assert.Contains(t, output, "proto-config-agent")
	assert.Contains(t, output, "responses")
	assert.Contains(t, output, "a2a")
	assert.Contains(t, output, "invocations")
	// Should NOT contain "mcp" since it's not in protocol_configuration
	assert.NotContains(t, output, "mcp")

	t.Logf("=== endpoint show (protocol_configuration) ===\n%s", output)
}

func TestResolveEndpointProtocols(t *testing.T) {
	tests := []struct {
		name     string
		endpoint *agent_api.AgentEndpoint
		want     []string
	}{
		{"nil endpoint", nil, nil},
		{"empty endpoint", &agent_api.AgentEndpoint{}, nil},
		{"protocol_configuration preferred over protocols", &agent_api.AgentEndpoint{
			Protocols: []agent_api.AgentEndpointProtocol{"activity"},
			ProtocolConfiguration: &agent_api.ProtocolConfiguration{
				Responses: &agent_api.ResponsesProtocolConfiguration{},
				MCP:       &agent_api.MCPProtocolConfiguration{},
			},
		}, []string{"responses", "mcp"}},
		{"fallback to protocols", &agent_api.AgentEndpoint{
			Protocols: []agent_api.AgentEndpointProtocol{"responses", "a2a"},
		}, []string{"responses", "a2a"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveEndpointProtocols(tt.endpoint)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetIsolationKind(t *testing.T) {
	tests := []struct {
		name     string
		endpoint *agent_api.AgentEndpoint
		want     string
	}{
		{"nil endpoint", nil, ""},
		{"empty schemes", &agent_api.AgentEndpoint{}, ""},
		{"entra", &agent_api.AgentEndpoint{
			AuthorizationSchemes: []agent_api.AgentEndpointAuthorizationScheme{
				{Type: "Entra", IsolationKeySource: &agent_api.IsolationKeySource{Kind: "Entra"}},
			},
		}, "Entra"},
		{"header", &agent_api.AgentEndpoint{
			AuthorizationSchemes: []agent_api.AgentEndpointAuthorizationScheme{
				{Type: "Entra", IsolationKeySource: &agent_api.IsolationKeySource{Kind: "Header"}},
			},
		}, "Header"},
		{"nil isolation source", &agent_api.AgentEndpoint{
			AuthorizationSchemes: []agent_api.AgentEndpointAuthorizationScheme{
				{Type: "BotService"},
			},
		}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getIsolationKind(tt.endpoint)
			assert.Equal(t, tt.want, got)
		})
	}
}
