// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"errors"
	"os"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
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
	assert.Contains(t, output, `"kind": "hosted"`)
	assert.Contains(t, output, `"protocols"`)
	assert.Contains(t, output, `"responses"`)

	t.Logf("=== endpoint show --output json ===\n%s", output)
}

func TestRunPromptEndpointShowUsesPersistedDeploymentEndpoint(t *testing.T) {
	props, err := structpb.NewStruct(map[string]any{
		"kind":         "prompt",
		"name":         "locally-edited-name",
		"model":        "gpt-5-mini",
		"instructions": "Help.",
	})
	require.NoError(t, err)
	svc := &azdext.ServiceConfig{
		Name:                 "assistant",
		Host:                 AiAgentHost,
		AdditionalProperties: props,
	}
	env := &testEnvironmentServiceServer{
		current: &azdext.Environment{Name: "dev"},
		values: map[string]map[string]string{"dev": {
			"AGENT_ASSISTANT_NAME":     "deployed-name",
			"AGENT_ASSISTANT_ENDPOINT": "https://deployed.example/responses",
			"AGENT_ASSISTANT_VERSION":  "3",
		}},
	}
	client := newHelpersTestAzdClient(t, &helpersProjectServer{}, &helpersPromptServer{}, env)

	output := captureEndpointOutput(t, func() error {
		return runPromptEndpointShow(
			t.Context(),
			client,
			svc,
			project.AgentDefinitionValidation{Kind: agent_yaml.AgentKindPrompt, Name: "locally-edited-name"},
			"json",
		)
	})

	assert.Contains(t, output, `"name": "deployed-name"`)
	assert.Contains(t, output, `"kind": "prompt"`)
	assert.Contains(
		t,
		output,
		`"responses": "https://deployed.example/responses"`,
	)
	assert.NotContains(t, output, `"agent_endpoint"`)
}

func TestRunPromptEndpointShowRequiresCompleteDeployment(t *testing.T) {
	tests := []struct {
		name    string
		values  map[string]string
		missing string
	}{
		{
			name: "missing endpoint",
			values: map[string]string{
				"AGENT_ASSISTANT_VERSION": "3",
			},
			missing: "AGENT_ASSISTANT_ENDPOINT",
		},
		{
			name: "missing version",
			values: map[string]string{
				"AGENT_ASSISTANT_ENDPOINT": "https://deployed.example/responses",
			},
			missing: "AGENT_ASSISTANT_VERSION",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := &testEnvironmentServiceServer{
				current: &azdext.Environment{Name: "dev"},
				values:  map[string]map[string]string{"dev": test.values},
			}
			client := newHelpersTestAzdClient(t, &helpersProjectServer{}, &helpersPromptServer{}, env)

			err := runPromptEndpointShow(
				t.Context(),
				client,
				&azdext.ServiceConfig{Name: "assistant", Host: AiAgentHost},
				project.AgentDefinitionValidation{Kind: agent_yaml.AgentKindPrompt, Name: "prompt-agent"},
				"json",
			)

			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			require.Equal(t, exterrors.CodeMissingAgentEnvVars, localErr.Code)
			require.Contains(t, localErr.Message, test.missing)
			require.Contains(t, localErr.Suggestion, "azd deploy")
		})
	}
}

func TestRunVoiceEndpointShowUsesDeployedVoiceEndpoint(t *testing.T) {
	env := &testEnvironmentServiceServer{
		current: &azdext.Environment{Name: "dev"},
		values: map[string]map[string]string{"dev": {
			"AGENT_VOICE_NAME":     "deployed-voice",
			"AGENT_VOICE_ENDPOINT": "wss://acct.example/voice",
		}},
	}
	client := newHelpersTestAzdClient(t, &helpersProjectServer{}, &helpersPromptServer{}, env)
	svc := &azdext.ServiceConfig{Name: "voice", Host: AiAgentHost}

	output := captureEndpointOutput(t, func() error {
		return runVoiceEndpointShow(
			t.Context(),
			client,
			svc,
			project.AgentDefinitionValidation{Kind: agent_yaml.AgentKindVoice, Name: "authored-voice"},
			"table",
		)
	})

	assert.Contains(t, output, "Agent:")
	assert.Contains(t, output, "deployed-voice")
	assert.Contains(t, output, "Kind:")
	assert.Contains(t, output, "voice")
	assert.Contains(t, output, "wss://acct.example/voice")
	assert.NotContains(t, output, "Version Selector")
}

func TestRunVoiceEndpointShowRequiresDeployedVoiceEndpoint(t *testing.T) {
	env := &testEnvironmentServiceServer{
		current: &azdext.Environment{Name: "dev"},
		values:  map[string]map[string]string{"dev": {}},
	}
	client := newHelpersTestAzdClient(t, &helpersProjectServer{}, &helpersPromptServer{}, env)
	svc := &azdext.ServiceConfig{Name: "voice", Host: AiAgentHost}

	err := runVoiceEndpointShow(
		t.Context(),
		client,
		svc,
		project.AgentDefinitionValidation{Kind: agent_yaml.AgentKindPromptVoice, Name: "voice-agent"},
		"json",
	)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Equal(t, exterrors.CodeMissingAgentEnvVars, localErr.Code)
	require.Contains(t, localErr.Message, "AGENT_VOICE_ENDPOINT")
	require.Contains(t, localErr.Suggestion, "azd deploy")
}

func TestRunEndpointShowRejectsWorkflowKind(t *testing.T) {
	props, err := structpb.NewStruct(map[string]any{
		"kind": "workflow",
		"name": "workflow-agent",
	})
	require.NoError(t, err)
	root := t.TempDir()
	svc := &azdext.ServiceConfig{
		Name:                 "workflow",
		Host:                 AiAgentHost,
		AdditionalProperties: props,
	}
	client := newHelpersTestAzdClient(t, &helpersProjectServer{project: &azdext.ProjectConfig{
		Path: root,
		Services: map[string]*azdext.ServiceConfig{
			svc.Name: svc,
		},
	}}, &helpersPromptServer{})

	err = runEndpointShow(
		t.Context(),
		client,
		&endpointShowFlags{name: svc.Name},
		&azdext.ExtensionContext{NoPrompt: true},
	)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Equal(t, exterrors.CodeUnsupportedAgentKind, localErr.Code)
	require.Contains(t, localErr.Message, "unsupported kind")
	require.Contains(t, localErr.Suggestion, "set kind")
}

func captureEndpointOutput(t *testing.T, run func() error) string {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	t.Cleanup(func() {
		os.Stdout = old
		_ = r.Close()
		_ = w.Close()
	})

	require.NoError(t, run())
	require.NoError(t, w.Close())
	os.Stdout = old

	var buf bytes.Buffer
	_, err = buf.ReadFrom(r)
	require.NoError(t, err)
	return buf.String()
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
		{"empty protocol_configuration is authoritative", &agent_api.AgentEndpoint{
			Protocols:             []agent_api.AgentEndpointProtocol{"activity"},
			ProtocolConfiguration: &agent_api.ProtocolConfiguration{},
		}, nil},
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
