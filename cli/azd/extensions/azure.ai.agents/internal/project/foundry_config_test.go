// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestFoundryProjectConfig_Validate(t *testing.T) {
	hostedImage := FoundryAgent{Name: "a", Kind: "hosted", Image: "reg.azurecr.io/a:1"}
	hostedRuntime := FoundryAgent{
		Name:           "a",
		Kind:           "hosted",
		Project:        "src/a",
		Runtime:        &AgentRuntime{Stack: "python", Version: "3.13"},
		StartupCommand: "python main.py",
	}

	tests := []struct {
		name      string
		config    FoundryProjectConfig
		wantErr   bool
		errSubstr string
	}{
		{name: "valid image", config: FoundryProjectConfig{Agents: []FoundryAgent{hostedImage}}},
		{name: "valid runtime", config: FoundryProjectConfig{Agents: []FoundryAgent{hostedRuntime}}},
		{name: "no agents", config: FoundryProjectConfig{}, wantErr: true, errSubstr: "no agents"},
		{
			name:      "multiple agents",
			config:    FoundryProjectConfig{Agents: []FoundryAgent{hostedImage, hostedRuntime}},
			wantErr:   true,
			errSubstr: "multiple agents",
		},
		{
			name:      "ref agent",
			config:    FoundryProjectConfig{Agents: []FoundryAgent{{Ref: "./a.yaml"}}},
			wantErr:   true,
			errSubstr: "$ref",
		},
		{
			name:      "missing name",
			config:    FoundryProjectConfig{Agents: []FoundryAgent{{Kind: "hosted", Image: "x"}}},
			wantErr:   true,
			errSubstr: "name",
		},
		{
			name:      "missing kind",
			config:    FoundryProjectConfig{Agents: []FoundryAgent{{Name: "a", Image: "x"}}},
			wantErr:   true,
			errSubstr: "kind",
		},
		{
			name:      "prompt unsupported",
			config:    FoundryProjectConfig{Agents: []FoundryAgent{{Name: "a", Kind: "prompt", Instructions: "hi"}}},
			wantErr:   true,
			errSubstr: "prompt",
		},
		{
			name:      "unknown kind",
			config:    FoundryProjectConfig{Agents: []FoundryAgent{{Name: "a", Kind: "wat"}}},
			wantErr:   true,
			errSubstr: "unsupported kind",
		},
		{
			name:      "hosted no deploy mode",
			config:    FoundryProjectConfig{Agents: []FoundryAgent{{Name: "a", Kind: "hosted"}}},
			wantErr:   true,
			errSubstr: "no deploy mode",
		},
		{
			name: "hosted multiple deploy modes",
			config: FoundryProjectConfig{Agents: []FoundryAgent{{
				Name: "a", Kind: "hosted", Image: "x", Runtime: &AgentRuntime{Stack: "python"},
			}}},
			wantErr:   true,
			errSubstr: "more than one deploy mode",
		},
		{
			name: "runtime missing project",
			config: FoundryProjectConfig{Agents: []FoundryAgent{{
				Name: "a", Kind: "hosted", Runtime: &AgentRuntime{Stack: "python", Version: "3.13"}, StartupCommand: "python main.py",
			}}},
			wantErr:   true,
			errSubstr: "project",
		},
		{
			name: "runtime missing startupCommand",
			config: FoundryProjectConfig{Agents: []FoundryAgent{{
				Name: "a", Kind: "hosted", Project: "src/a", Runtime: &AgentRuntime{Stack: "python", Version: "3.13"},
			}}},
			wantErr:   true,
			errSubstr: "startupCommand",
		},
		{
			name: "runtime unsupported stack",
			config: FoundryProjectConfig{Agents: []FoundryAgent{{
				Name: "a", Kind: "hosted", Project: "src/a",
				Runtime: &AgentRuntime{Stack: "node", Version: "20"}, StartupCommand: "node index.js",
			}}},
			wantErr:   true,
			errSubstr: "not supported",
		},
		{
			name: "docker unsupported",
			config: FoundryProjectConfig{Agents: []FoundryAgent{{
				Name: "a", Kind: "hosted", Project: "src/a", Docker: &AgentDocker{Path: "Dockerfile"},
			}}},
			wantErr:   true,
			errSubstr: "does not support yet",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent, err := tt.config.Validate()
			if tt.wantErr {
				require.Error(t, err)
				if tt.errSubstr != "" {
					assert.Contains(t, err.Error(), tt.errSubstr)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "a", agent.Name)
		})
	}
}

func TestFoundryAgent_toContainerAgent_Image(t *testing.T) {
	desc := "an agent"
	agent := FoundryAgent{
		Name:        "support",
		Kind:        "hosted",
		Description: desc,
		Image:       "reg.azurecr.io/support:1",
		Protocols:   []AgentProtocol{{Protocol: "responses", Version: "1.0.0"}},
		Metadata:    map[string]any{"team": "cx"},
	}

	ca, err := agent.toContainerAgent()
	require.NoError(t, err)

	assert.Equal(t, agent_yaml.AgentKindHosted, ca.Kind)
	assert.Equal(t, "support", ca.Name)
	assert.Equal(t, "reg.azurecr.io/support:1", ca.Image)
	require.NotNil(t, ca.Description)
	assert.Equal(t, desc, *ca.Description)
	assert.Nil(t, ca.CodeConfiguration)
	require.Len(t, ca.Protocols, 1)
	assert.Equal(t, "responses", ca.Protocols[0].Protocol)
	require.NotNil(t, ca.Metadata)
	assert.Equal(t, "cx", (*ca.Metadata)["team"])
}

func TestFoundryAgent_toContainerAgent_Runtime(t *testing.T) {
	agent := FoundryAgent{
		Name:           "code",
		Kind:           "hosted",
		Project:        "src/code",
		Runtime:        &AgentRuntime{Stack: "python", Version: "3.13"},
		StartupCommand: "python main.py",
	}

	ca, err := agent.toContainerAgent()
	require.NoError(t, err)

	require.NotNil(t, ca.CodeConfiguration)
	assert.Equal(t, "python_3_13", ca.CodeConfiguration.Runtime)
	assert.Equal(t, "main.py", ca.CodeConfiguration.EntryPoint)
	assert.Empty(t, ca.Image)
}

func TestRuntimeString(t *testing.T) {
	assert.Equal(t, "python_3_13", runtimeString(&AgentRuntime{Stack: "python", Version: "3.13"}))
	assert.Equal(t, "dotnet_8", runtimeString(&AgentRuntime{Stack: "dotnet", Version: "8"}))
	assert.Equal(t, "python", runtimeString(&AgentRuntime{Stack: "python"}))
	assert.Equal(t, "", runtimeString(nil))
}

func TestFoundryAgent_codeEntryPoint(t *testing.T) {
	tests := []struct {
		name    string
		agent   FoundryAgent
		want    string
		wantErr bool
	}{
		{
			name: "strips python prefix",
			agent: FoundryAgent{
				Runtime: &AgentRuntime{Stack: "python", Version: "3.13"}, StartupCommand: "python main.py",
			},
			want: "main.py",
		},
		{
			name: "strips dotnet prefix",
			agent: FoundryAgent{
				Runtime: &AgentRuntime{Stack: "dotnet", Version: "8"}, StartupCommand: "dotnet MyAgent.dll",
			},
			want: "MyAgent.dll",
		},
		{
			name: "no prefix keeps command",
			agent: FoundryAgent{
				Runtime: &AgentRuntime{Stack: "python", Version: "3.13"}, StartupCommand: "main.py",
			},
			want: "main.py",
		},
		{
			name:    "empty command errors",
			agent:   FoundryAgent{Runtime: &AgentRuntime{Stack: "python", Version: "3.13"}, StartupCommand: "   "},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.agent.codeEntryPoint()
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFoundryAgent_resolvedEnv(t *testing.T) {
	agent := FoundryAgent{
		Env: map[string]string{
			"PLAIN":     "value",
			"FROM_AZD":  "${MY_VAR}",
			"FOUNDRY":   "${{connections.x.key}}",
			"MIXED":     "${MY_VAR}-${{event.body}}",
			"UNDEFINED": "${MISSING}",
		},
	}

	resolved := agent.resolvedEnv(map[string]string{"MY_VAR": "hello"})

	assert.Equal(t, "value", resolved["PLAIN"])
	assert.Equal(t, "hello", resolved["FROM_AZD"])
	assert.Equal(t, "${{connections.x.key}}", resolved["FOUNDRY"])
	assert.Equal(t, "hello-${{event.body}}", resolved["MIXED"])
	assert.Equal(t, "", resolved["UNDEFINED"])
}

func TestFoundryAgent_resolvedEnv_Empty(t *testing.T) {
	assert.Nil(t, FoundryAgent{}.resolvedEnv(nil))
}

// TestFoundryProjectConfig_BindFromAdditionalProperties verifies the config binds
// from a structpb.Struct the way core delivers AdditionalProperties over gRPC.
func TestFoundryProjectConfig_BindFromAdditionalProperties(t *testing.T) {
	raw := map[string]any{
		"endpoint": "https://acct.services.ai.azure.com/api/projects/p",
		"deployments": []any{
			map[string]any{"name": "gpt-4.1-mini"},
		},
		"agents": []any{
			map[string]any{
				"name":           "basic-agent",
				"kind":           "hosted",
				"description":    "A basic agent.",
				"project":        "src/basic-agent",
				"startupCommand": "python main.py",
				"runtime":        map[string]any{"stack": "python", "version": "3.13"},
				"protocols": []any{
					map[string]any{"protocol": "responses", "version": "1.0.0"},
				},
				"env": map[string]any{"FOUNDRY_MODEL_DEPLOYMENT_NAME": "gpt-4.1-mini"},
			},
		},
	}

	s, err := structpb.NewStruct(raw)
	require.NoError(t, err)

	var config *FoundryProjectConfig
	require.NoError(t, UnmarshalStruct(s, &config))
	require.NotNil(t, config)

	assert.Equal(t, "https://acct.services.ai.azure.com/api/projects/p", config.Endpoint)
	assert.Len(t, config.Deployments, 1)

	agent, err := config.Validate()
	require.NoError(t, err)
	assert.Equal(t, "basic-agent", agent.Name)
	assert.Equal(t, deployModeRuntime, agent.deployMode())
	require.NotNil(t, agent.Runtime)
	assert.Equal(t, "python", agent.Runtime.Stack)
	assert.Equal(t, "gpt-4.1-mini", agent.Env["FOUNDRY_MODEL_DEPLOYMENT_NAME"])
}
