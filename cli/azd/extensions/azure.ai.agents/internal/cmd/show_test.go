// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShowCommand_AcceptsPositionalArg(t *testing.T) {
	cmd := newShowCommand(nil)
	err := cmd.Args(cmd, []string{"my-agent"})
	assert.NoError(t, err)
}

func TestShowCommand_AcceptsNoArgs(t *testing.T) {
	cmd := newShowCommand(nil)
	err := cmd.Args(cmd, []string{})
	assert.NoError(t, err)
}

func TestShowCommand_RejectsMultipleArgs(t *testing.T) {
	cmd := newShowCommand(nil)
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

func TestShowCommand_DefaultOutputFormat(t *testing.T) {
	cmd := newShowCommand(nil)
	assertOutputFlagOptions(t, cmd, "table", []string{"json", "table"})
}

func TestPrintShowResult_DefaultsToTable(t *testing.T) {
	output, err := captureStdout(t, func() error {
		return printShowResult(sampleShowResult(), "", nil)
	})
	require.NoError(t, err)

	assert.Contains(t, output, "FIELD")
	assert.Contains(t, output, "VALUE")
	assert.Contains(t, output, "sample-agent")
	assert.NotContains(t, output, `"object"`)
}

func TestPrintShowResult_JSONOptIn(t *testing.T) {
	output, err := captureStdout(t, func() error {
		return printShowResult(sampleShowResult(), "json", nil)
	})
	require.NoError(t, err)

	assert.Contains(t, output, `"object": "agent.version"`)
	assert.Contains(t, output, `"name": "sample-agent"`)
}

func TestPrintShowResult_ExplicitTable(t *testing.T) {
	output, err := captureStdout(t, func() error {
		return printShowResult(sampleShowResult(), "table", nil)
	})
	require.NoError(t, err)

	assert.Contains(t, output, "FIELD")
	assert.Contains(t, output, "VALUE")
	assert.Contains(t, output, "sample-agent")
	assert.NotContains(t, output, `"object"`)
}

func TestPrintShowResult_UnsupportedOutput(t *testing.T) {
	err := printShowResult(sampleShowResult(), "yaml", nil)
	assert.EqualError(t, err, `unsupported output format "yaml"`)
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
	_, hasNextStep := raw["next_step"]
	assert.False(t, hasNextStep, "next_step should be omitted when nil")
}

func TestShowResultJSON_NextStepEnvelope(t *testing.T) {
	version := &agent_api.AgentVersionObject{
		Object:  "agent.version",
		ID:      "ver-999",
		Name:    "my-agent",
		Version: "1",
		Status:  "active",
	}

	result := &showResult{
		AgentVersionObject: version,
		NextStep: toNextStepEnvelope([]nextstep.Suggestion{
			{
				Command:     `azd ai agent invoke my-agent "Hello!"`,
				Description: "the agent is ready — send it a sample request",
				Priority:    10,
			},
		}),
	}

	jsonBytes, err := json.MarshalIndent(result, "", "  ")
	require.NoError(t, err)

	var raw map[string]any
	err = json.Unmarshal(jsonBytes, &raw)
	require.NoError(t, err)

	nextStep, ok := raw["next_step"].(map[string]any)
	require.True(t, ok, "next_step should be present and an object")
	suggestions, ok := nextStep["suggestions"].([]any)
	require.True(t, ok, "next_step.suggestions should be an array")
	require.Len(t, suggestions, 1)
	first := suggestions[0].(map[string]any)
	assert.Equal(t, `azd ai agent invoke my-agent "Hello!"`, first["command"])
	assert.Equal(t, "the agent is ready — send it a sample request", first["description"])
	// Internal renderer hints (priority, trailing) must not leak into JSON.
	_, hasPriority := first["priority"]
	assert.False(t, hasPriority, "priority must not appear in JSON envelope")
	_, hasTrailing := first["trailing"]
	assert.False(t, hasTrailing, "trailing must not appear in JSON envelope")
}

func TestToNextStepEnvelope_EmptyReturnsNil(t *testing.T) {
	assert.Nil(t, toNextStepEnvelope(nil))
	assert.Nil(t, toNextStepEnvelope([]nextstep.Suggestion{}))
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

	err := printShowResultTable(result, nil)
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
	err := printShowResultTable(result, nil)
	require.NoError(t, err)
}

func sampleShowResult() *showResult {
	return &showResult{
		AgentVersionObject: &agent_api.AgentVersionObject{
			Object:  "agent.version",
			ID:      "ver-sample",
			Name:    "sample-agent",
			Version: "1",
		},
	}
}

func captureStdout(t *testing.T, run func() error) (string, error) {
	t.Helper()

	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	defer func() {
		os.Stdout = oldStdout
	}()
	defer reader.Close()

	os.Stdout = writer
	runErr := run()
	require.NoError(t, writer.Close())

	output, err := io.ReadAll(reader)
	require.NoError(t, err)

	return string(output), runErr
}

// fakeShowSource is a minimal nextstep.Source for wiring tests.
// It returns canned project/env data without touching the real azd
// gRPC client. Only the surfaces actually exercised by AssembleState
// are populated.
type fakeShowSource struct {
	envName string
	project *azdext.ProjectConfig
	values  map[string]string
}

func (f *fakeShowSource) CurrentEnvName(_ context.Context) (string, error) {
	return f.envName, nil
}

func (f *fakeShowSource) Project(_ context.Context) (*azdext.ProjectConfig, error) {
	return f.project, nil
}

func (f *fakeShowSource) EnvValue(_ context.Context, envName, key string) (string, error) {
	return f.values[envName+"/"+key], nil
}

// TestResolveNextStepFromSource_ActiveBranch_InvocationsProtocol exercises
// the full show → resolver wiring end-to-end: AssembleState reads the
// service's agent.yaml (via the fake project root in a t.TempDir) to
// detect the invocations protocol, then ResolveAfterShow emits the
// protocol-aware invoke suggestion using the Foundry agent name.
func TestResolveNextStepFromSource_ActiveBranch_InvocationsProtocol(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	svcDir := filepath.Join(projectRoot, "src", "echo-svc")
	require.NoError(t, os.MkdirAll(svcDir, 0o750))
	agentYAML := []byte(`
protocols:
  - protocol: invocations
    version: "1"
`)
	require.NoError(t, os.WriteFile(filepath.Join(svcDir, "agent.yaml"), agentYAML, 0o600))

	src := &fakeShowSource{
		envName: "dev",
		project: &azdext.ProjectConfig{
			Name: "demo",
			Path: projectRoot,
			Services: map[string]*azdext.ServiceConfig{
				"echo-svc": {
					Name:         "echo-svc",
					Host:         "azure.ai.agent",
					RelativePath: filepath.Join("src", "echo-svc"),
				},
			},
		},
	}

	out := resolveNextStepFromSource(t.Context(), src, "echo-svc", "echo-deployed-x7q9", "active")
	require.Len(t, out, 1)
	assert.Equal(t,
		`azd ai agent invoke echo-svc '{"message": "Hello!"}'`,
		out[0].Command,
		"Active branch should emit protocol-aware invoke command using the azure.yaml service name "+
			"(invoke.go translates to the deployed agent name internally)")
}

// TestResolveNextStepFromSource_UnknownStatusFallsBackToServiceName locks
// the unknown-status branch: when the resolver can't classify the status,
// it suggests `azd ai agent show <serviceName>` (not agentName), because
// show.go's lookup matches by service name.
func TestResolveNextStepFromSource_UnknownStatusFallsBackToServiceName(t *testing.T) {
	t.Parallel()

	src := &fakeShowSource{
		envName: "dev",
		project: &azdext.ProjectConfig{Name: "demo"},
	}

	out := resolveNextStepFromSource(t.Context(), src, "echo-svc", "echo-deployed-x7q9", "Transitioning")
	require.Len(t, out, 1)
	assert.Equal(t, "azd ai agent show echo-svc", out[0].Command)
}

// TestResolveNextStepFromSource_NonActiveBranches sanity-checks the
// remaining status branches don't depend on either service or agent name.
func TestResolveNextStepFromSource_NonActiveBranches(t *testing.T) {
	t.Parallel()

	src := &fakeShowSource{
		envName: "dev",
		project: &azdext.ProjectConfig{Name: "demo"},
	}

	tests := []struct {
		status string
		want   string
	}{
		{"creating", "azd ai agent monitor --type system --follow"},
		{"failed", "azd ai agent monitor --follow"},
		{"", "azd ai agent monitor --follow"},
		{"deleting", "azd deploy"},
		{"deleted", "azd deploy"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.status, func(t *testing.T) {
			t.Parallel()
			out := resolveNextStepFromSource(t.Context(), src, "echo-svc", "echo-deployed-x7q9", tt.status)
			require.Len(t, out, 1)
			assert.Equal(t, tt.want, out[0].Command)
		})
	}
}
