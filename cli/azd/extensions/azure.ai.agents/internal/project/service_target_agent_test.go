// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestApplyAgentMetadata(t *testing.T) {
	tests := []struct {
		name         string
		existingMeta map[string]string
	}{
		{
			name: "nil metadata initialized",
		},
		{
			name:         "preserves existing metadata",
			existingMeta: map[string]string{"authors": "test"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := &agent_api.CreateAgentRequest{
				Name: "test-agent",
				CreateAgentVersionRequest: agent_api.CreateAgentVersionRequest{
					Metadata: tt.existingMeta,
				},
			}

			applyAgentMetadata(request)

			val, exists := request.Metadata["enableVnextExperience"]
			if !exists || val != "true" {
				t.Errorf("expected enableVnextExperience=true in metadata, got exists=%v val=%q", exists, val)
			}

			// Verify existing metadata is preserved
			if tt.existingMeta != nil {
				for k, v := range tt.existingMeta {
					if request.Metadata[k] != v {
						t.Errorf("existing metadata key %q was lost or changed: want %q, got %q", k, v, request.Metadata[k])
					}
				}
			}
		})
	}
}

func TestGetServiceKey_NormalizesToolboxNames(t *testing.T) {
	t.Parallel()

	p := &AgentServiceTargetProvider{}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"hyphens", "agent-tools", "AGENT_TOOLS"},
		{"spaces", "agent tools", "AGENT_TOOLS"},
		{"mixed", "my-agent tools", "MY_AGENT_TOOLS"},
		{"already upper", "TOOLS", "TOOLS"},
		{"lowercase", "tools", "TOOLS"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.getServiceKey(tt.input)
			if got != tt.expected {
				t.Errorf("getServiceKey(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// --- helpers for Package tests ---

// writeHostedAgentYAML creates a minimal hosted-kind agent.yaml in dir.
func writeHostedAgentYAML(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "agent.yaml")
	require.NoError(t, os.WriteFile(p, []byte("kind: hosted\nname: test-agent\n"), 0o600))
	return p
}

// stubContainerServer is a minimal ContainerServiceServer that returns
// success responses for Build and Package.
type stubContainerServer struct {
	azdext.UnimplementedContainerServiceServer
}

func (s *stubContainerServer) Build(_ context.Context, _ *azdext.ContainerBuildRequest) (*azdext.ContainerBuildResponse, error) {
	return &azdext.ContainerBuildResponse{
		Result: &azdext.ServiceBuildResult{
			Artifacts: []*azdext.Artifact{{
				Kind:     azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER,
				Location: "test-image:latest",
			}},
		},
	}, nil
}

func (s *stubContainerServer) Package(_ context.Context, _ *azdext.ContainerPackageRequest) (*azdext.ContainerPackageResponse, error) {
	return &azdext.ContainerPackageResponse{
		Result: &azdext.ServicePackageResult{
			Artifacts: []*azdext.Artifact{{
				Kind:     azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER,
				Location: "myregistry.azurecr.io/test-image:latest",
			}},
		},
	}, nil
}

// newContainerTestClient spins up a gRPC server with the given container
// service and returns an AzdClient connected to it.
func newContainerTestClient(t *testing.T, containerSrv azdext.ContainerServiceServer) *azdext.AzdClient {
	t.Helper()

	srv := grpc.NewServer()
	azdext.RegisterContainerServiceServer(srv, containerSrv)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.Stop()
		_ = lis.Close()
	})

	client, err := azdext.NewAzdClient(azdext.WithAddress(lis.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	return client
}

// stubEnvServer records SetValue calls for testing registerAgentEnvironmentVariables.
type stubEnvServer struct {
	azdext.UnimplementedEnvironmentServiceServer
	values map[string]string
}

func (s *stubEnvServer) SetValue(
	_ context.Context, req *azdext.SetEnvRequest,
) (*azdext.EmptyResponse, error) {
	if s.values == nil {
		s.values = make(map[string]string)
	}
	s.values[req.Key] = req.Value
	return &azdext.EmptyResponse{}, nil
}

// newEnvTestClient spins up a gRPC server with the given environment
// service stub and returns an AzdClient connected to it.
func newEnvTestClient(
	t *testing.T, envSrv azdext.EnvironmentServiceServer,
) *azdext.AzdClient {
	t.Helper()

	srv := grpc.NewServer()
	azdext.RegisterEnvironmentServiceServer(srv, envSrv)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.Stop()
		_ = lis.Close()
	})

	client, err := azdext.NewAzdClient(
		azdext.WithAddress(lis.Addr().String()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	return client
}

func TestRegisterAgentEnvironmentVariables(t *testing.T) {
	t.Parallel()

	envStub := &stubEnvServer{}
	client := newEnvTestClient(t, envStub)

	provider := &AgentServiceTargetProvider{
		azdClient: client,
		env:       &azdext.Environment{Name: "test-env"},
	}

	azdEnv := map[string]string{
		"AZURE_AI_PROJECT_ENDPOINT": "https://proj.azure.com",
	}
	protocols := []agent_yaml.ProtocolVersionRecord{
		{Protocol: "responses", Version: "1.0.0"},
		{Protocol: "invocations", Version: "1.0.0"},
	}
	agentVersion := &agent_api.AgentVersionObject{
		Name:    "my-agent",
		Version: "1.0.0",
	}

	err := provider.registerAgentEnvironmentVariables(
		t.Context(), azdEnv,
		&azdext.ServiceConfig{Name: "my-svc"},
		agentVersion,
		protocols,
	)
	require.NoError(t, err)

	// Verify per-protocol env vars
	require.Contains(t, envStub.values, "AGENT_MY_SVC_NAME")
	require.Equal(t, "my-agent", envStub.values["AGENT_MY_SVC_NAME"])
	require.Contains(t, envStub.values, "AGENT_MY_SVC_VERSION")
	require.Equal(t, "1.0.0", envStub.values["AGENT_MY_SVC_VERSION"])

	// Per-protocol endpoints
	require.Contains(t, envStub.values, "AGENT_MY_SVC_RESPONSES_ENDPOINT")
	require.Contains(t,
		envStub.values["AGENT_MY_SVC_RESPONSES_ENDPOINT"],
		"/agents/my-agent/endpoint/protocols/openai/responses")
	require.Contains(t, envStub.values, "AGENT_MY_SVC_INVOCATIONS_ENDPOINT")
	require.Contains(t,
		envStub.values["AGENT_MY_SVC_INVOCATIONS_ENDPOINT"],
		"/agents/my-agent/endpoint/protocols/invocations")

	// Legacy env var cleared
	require.Contains(t, envStub.values, "AGENT_MY_SVC_ENDPOINT")
	require.Empty(t, envStub.values["AGENT_MY_SVC_ENDPOINT"])
}

func TestProtocolPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		protocol string
		expected string
	}{
		{"responses", "responses", "openai/responses"},
		{"invocations", "invocations", "invocations"},
		{"activity_protocol excluded", "activity_protocol", ""},
		{"unknown excluded", "unknown_proto", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := protocolPath(tt.protocol)
			require.Equal(t, tt.expected, got)
		})
	}
}

func TestAgentInvocationEndpoints(t *testing.T) {
	t.Parallel()

	const endpoint = "https://myproject.services.ai.azure.com"
	const agentName = "my-agent"
	baseURL := endpoint + "/agents/" + agentName + "/endpoint/protocols/"

	tests := []struct {
		name      string
		protocols []agent_yaml.ProtocolVersionRecord
		expected  []protocolEndpointInfo
	}{
		{
			name: "single responses protocol",
			protocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "responses", Version: "1.0.0"},
			},
			expected: []protocolEndpointInfo{
				{
					Protocol: "responses",
					URL:      baseURL + "openai/responses?api-version=" + agentAPIVersion,
				},
			},
		},
		{
			name: "single invocations protocol",
			protocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "invocations", Version: "1.0.0"},
			},
			expected: []protocolEndpointInfo{
				{
					Protocol: "invocations",
					URL:      baseURL + "invocations?api-version=" + agentAPIVersion,
				},
			},
		},
		{
			name: "multiple protocols with activity_protocol excluded",
			protocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "responses", Version: "1.0.0"},
				{Protocol: "activity_protocol", Version: "1.0.0"},
				{Protocol: "invocations", Version: "1.0.0"},
			},
			expected: []protocolEndpointInfo{
				{
					Protocol: "responses",
					URL:      baseURL + "openai/responses?api-version=" + agentAPIVersion,
				},
				{
					Protocol: "invocations",
					URL:      baseURL + "invocations?api-version=" + agentAPIVersion,
				},
			},
		},
		{
			name: "only activity_protocol yields empty",
			protocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "activity_protocol", Version: "1.0.0"},
			},
			expected: nil,
		},
		{
			name:      "nil protocols yields empty",
			protocols: nil,
			expected:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agentInvocationEndpoints(endpoint, agentName, tt.protocols)
			require.Equal(t, tt.expected, got)
		})
	}
}

func TestDeployArtifacts_HostedAgent_ProtocolEndpoints(t *testing.T) {
	t.Parallel()

	p := &AgentServiceTargetProvider{}
	const ep = "https://myproject.services.ai.azure.com"

	protocols := []agent_yaml.ProtocolVersionRecord{
		{Protocol: "responses", Version: "1.0.0"},
		{Protocol: "invocations", Version: "1.0.0"},
	}

	artifacts := p.deployArtifacts(
		"test-agent", "1.0.0",
		"", // no project resource ID — skip playground
		ep,
		protocols,
	)

	// Should have 2 endpoint artifacts (one per displayable protocol)
	require.Len(t, artifacts, 2)

	wantResponses := ep +
		"/agents/test-agent/endpoint/protocols/openai/responses" +
		"?api-version=" + agentAPIVersion
	require.Equal(t, wantResponses, artifacts[0].Location)
	require.Equal(t, "Agent endpoint (responses)", artifacts[0].Metadata["label"])
	require.Empty(t, artifacts[0].Metadata["note"],
		"note should only appear on the last endpoint")

	wantInvocations := ep +
		"/agents/test-agent/endpoint/protocols/invocations" +
		"?api-version=" + agentAPIVersion
	require.Equal(t, wantInvocations, artifacts[1].Location)
	require.Equal(t, "Agent endpoint (invocations)", artifacts[1].Metadata["label"])
	require.Contains(t, artifacts[1].Metadata["note"], "invoking the agent")
}

func TestDeployArtifacts_ResponsesProtocol(t *testing.T) {
	t.Parallel()

	p := &AgentServiceTargetProvider{}
	const ep = "https://myproject.services.ai.azure.com"

	protocols := []agent_yaml.ProtocolVersionRecord{
		{Protocol: "responses", Version: "1.0.0"},
	}

	artifacts := p.deployArtifacts(
		"prompt-agent", "2.0.0",
		"", // no project resource ID — skip playground
		ep,
		protocols,
	)

	require.Len(t, artifacts, 1)
	wantURL := ep +
		"/agents/prompt-agent/endpoint/protocols/openai/responses" +
		"?api-version=" + agentAPIVersion
	require.Equal(t, wantURL, artifacts[0].Location)
	require.Equal(t, "Agent endpoint (responses)", artifacts[0].Metadata["label"])
	require.Contains(t, artifacts[0].Metadata["note"], "invoking the agent")
}

func TestDeployArtifacts_EmptyProtocols_NoEndpoints(t *testing.T) {
	t.Parallel()

	p := &AgentServiceTargetProvider{}

	// When protocols is empty, no endpoint artifacts are produced.
	artifacts := p.deployArtifacts(
		"agent", "1.0.0",
		"", "https://ep.azure.com",
		nil,
	)
	require.Empty(t, artifacts)
}

// TestPackage_NoEarlyFailureWithoutACR is a regression test ensuring that
// Package for a hosted agent does not fail early when
// AZURE_CONTAINER_REGISTRY_ENDPOINT is unset. The ACR endpoint is resolved
// later by the azd core container service, not by the extension.
func TestPackage_NoEarlyFailureWithoutACR(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	agentPath := writeHostedAgentYAML(t, dir)

	client := newContainerTestClient(t, &stubContainerServer{})

	provider := &AgentServiceTargetProvider{
		azdClient:           client,
		agentDefinitionPath: agentPath,
		env:                 &azdext.Environment{Name: "test-env"},
	}

	result, err := provider.Package(
		t.Context(),
		&azdext.ServiceConfig{Name: "test-svc"},
		&azdext.ServiceContext{},
		func(string) {},
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.Artifacts, "expected container artifacts from Package")
}

func TestAgentPlaygroundURL_Valid(t *testing.T) {
	t.Parallel()

	// A valid ARM resource ID for a Foundry project
	projectResourceID := "/subscriptions/00000000-0000-0000-0000-000000000001/" +
		"resourceGroups/my-rg/providers/Microsoft.CognitiveServices/" +
		"accounts/my-account/projects/my-project"

	url, err := AgentPlaygroundURL(projectResourceID, "test-agent", "3")
	require.NoError(t, err)
	require.NotEmpty(t, url)
	require.Contains(t, url, "ai.azure.com/nextgen/r/")
	require.Contains(t, url, "my-rg")
	require.Contains(t, url, "my-account")
	require.Contains(t, url, "my-project")
	require.Contains(t, url, "test-agent")
	require.Contains(t, url, "version=3")
}

func TestAgentPlaygroundURL_InvalidResourceID(t *testing.T) {
	t.Parallel()

	_, err := AgentPlaygroundURL("not-a-valid-resource-id", "agent", "1")
	require.Error(t, err)
}

func TestAgentPlaygroundURL_EmptyInput(t *testing.T) {
	t.Parallel()

	// An empty string should fail ARM parsing
	_, err := AgentPlaygroundURL("", "agent", "1")
	require.Error(t, err)
}

func TestAgentPlaygroundURL_AccountLevelID(t *testing.T) {
	t.Parallel()

	// An account-level resource ID (no /projects/ child) should be rejected
	// because it would produce a malformed playground URL.
	resourceID := "/subscriptions/00000000-0000-0000-0000-000000000001/" +
		"resourceGroups/my-rg/providers/Microsoft.CognitiveServices/accounts/my-account"

	_, err := AgentPlaygroundURL(resourceID, "agent", "1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing parent account")
}
