// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
// success responses for Build, Package, and Publish.
type stubContainerServer struct {
	azdext.UnimplementedContainerServiceServer
	buildCalls   atomic.Int32
	packageCalls atomic.Int32
	publishCalls atomic.Int32
	publishErr   error
}

func (s *stubContainerServer) Build(
	_ context.Context,
	_ *azdext.ContainerBuildRequest,
) (*azdext.ContainerBuildResponse, error) {
	s.buildCalls.Add(1)
	return &azdext.ContainerBuildResponse{
		Result: &azdext.ServiceBuildResult{
			Artifacts: []*azdext.Artifact{{
				Kind:     azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER,
				Location: "test-image:latest",
			}},
		},
	}, nil
}

func (s *stubContainerServer) Package(
	_ context.Context,
	_ *azdext.ContainerPackageRequest,
) (*azdext.ContainerPackageResponse, error) {
	s.packageCalls.Add(1)
	return &azdext.ContainerPackageResponse{
		Result: &azdext.ServicePackageResult{
			Artifacts: []*azdext.Artifact{{
				Kind:     azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER,
				Location: "myregistry.azurecr.io/test-image:latest",
			}},
		},
	}, nil
}

func (s *stubContainerServer) Publish(
	_ context.Context,
	_ *azdext.ContainerPublishRequest,
) (*azdext.ContainerPublishResponse, error) {
	s.publishCalls.Add(1)
	if s.publishErr != nil {
		return nil, s.publishErr
	}

	return &azdext.ContainerPublishResponse{
		Result: &azdext.ServicePublishResult{
			Artifacts: []*azdext.Artifact{{
				Kind:         azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER,
				Location:     "myregistry.azurecr.io/test-image:latest",
				LocationKind: azdext.LocationKind_LOCATION_KIND_REMOTE,
			}},
		},
	}, nil
}

// newContainerTestClient spins up a gRPC server with the given container
// service and returns an AzdClient connected to it.
func newContainerTestClient(t *testing.T, containerSrv azdext.ContainerServiceServer) *azdext.AzdClient {
	t.Helper()
	return newServiceTargetTestClient(t, containerSrv, nil)
}

func newServiceTargetTestClient(
	t *testing.T,
	containerSrv azdext.ContainerServiceServer,
	promptSrv azdext.PromptServiceServer,
) *azdext.AzdClient {
	t.Helper()

	srv := grpc.NewServer()
	if containerSrv != nil {
		azdext.RegisterContainerServiceServer(srv, containerSrv)
	}
	if promptSrv != nil {
		azdext.RegisterPromptServiceServer(srv, promptSrv)
	}

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

type stubPromptServer struct {
	azdext.UnimplementedPromptServiceServer
	selectedIndex int32
	selectCalls   atomic.Int32
	lastSelect    *azdext.SelectRequest
	err           error
}

func (s *stubPromptServer) Select(
	_ context.Context,
	req *azdext.SelectRequest,
) (*azdext.SelectResponse, error) {
	s.selectCalls.Add(1)
	s.lastSelect = req
	if s.err != nil {
		return nil, s.err
	}
	return &azdext.SelectResponse{Value: &s.selectedIndex}, nil
}

func newPromptTestClient(t *testing.T, promptSrv azdext.PromptServiceServer) *azdext.AzdClient {
	t.Helper()
	return newServiceTargetTestClient(t, nil, promptSrv)
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

	// Base agent endpoint for session management
	require.Contains(t, envStub.values, "AGENT_MY_SVC_ENDPOINT")
	require.Equal(t, "https://proj.azure.com/agents/my-agent/versions/1.0.0", envStub.values["AGENT_MY_SVC_ENDPOINT"])
}

func TestRegisterAgentEnvironmentVariables_TrailingSlash(t *testing.T) {
	t.Parallel()

	envStub := &stubEnvServer{}
	client := newEnvTestClient(t, envStub)

	provider := &AgentServiceTargetProvider{
		azdClient: client,
		env:       &azdext.Environment{Name: "test-env"},
	}

	azdEnv := map[string]string{
		"AZURE_AI_PROJECT_ENDPOINT": "https://proj.azure.com/",
	}
	protocols := []agent_yaml.ProtocolVersionRecord{
		{Protocol: "responses", Version: "1.0.0"},
	}
	agentVersion := &agent_api.AgentVersionObject{
		Name:    "my-agent",
		Version: "2.0.0",
	}

	err := provider.registerAgentEnvironmentVariables(
		t.Context(), azdEnv,
		&azdext.ServiceConfig{Name: "my-svc"},
		agentVersion,
		protocols,
	)
	require.NoError(t, err)

	// Trailing slash must not produce a double-slash in the base endpoint
	require.Equal(t, "https://proj.azure.com/agents/my-agent/versions/2.0.0", envStub.values["AGENT_MY_SVC_ENDPOINT"])
}

func TestRegisterAgentEnvironmentVariables_EmptyName(t *testing.T) {
	t.Parallel()

	envStub := &stubEnvServer{}
	client := newEnvTestClient(t, envStub)

	provider := &AgentServiceTargetProvider{
		azdClient: client,
		env:       &azdext.Environment{Name: "test-env"},
	}

	err := provider.registerAgentEnvironmentVariables(
		t.Context(),
		map[string]string{"AZURE_AI_PROJECT_ENDPOINT": "https://proj.azure.com"},
		&azdext.ServiceConfig{Name: "my-svc"},
		&agent_api.AgentVersionObject{Name: "", Version: "1.0.0"},
		nil,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "agent name is empty")
}

func TestRegisterAgentEnvironmentVariables_EmptyVersion(t *testing.T) {
	t.Parallel()

	envStub := &stubEnvServer{}
	client := newEnvTestClient(t, envStub)

	provider := &AgentServiceTargetProvider{
		azdClient: client,
		env:       &azdext.Environment{Name: "test-env"},
	}

	err := provider.registerAgentEnvironmentVariables(
		t.Context(),
		map[string]string{"AZURE_AI_PROJECT_ENDPOINT": "https://proj.azure.com"},
		&azdext.ServiceConfig{Name: "my-svc"},
		&agent_api.AgentVersionObject{Name: "my-agent", Version: ""},
		nil,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "agent version is empty")
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

// writeHostedAgentYAMLWithImage creates a hosted agent.yaml with a pre-built image field.
func writeHostedAgentYAMLWithImage(t *testing.T, dir, image string) string {
	t.Helper()
	p := filepath.Join(dir, "agent.yaml")
	content := fmt.Sprintf(
		"kind: hosted\nname: test-agent\nimage: %s\nprotocols:\n  - protocol: invocations\n    version: 1.0.0\n",
		image,
	)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	return p
}

func TestLoadContainerAgentDefinition_MalformedYAMLReturnsError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	agentPath := filepath.Join(dir, "agent.yaml")
	require.NoError(t, os.WriteFile(agentPath, []byte("kind: hosted\nname: [\n"), 0o600))

	provider := &AgentServiceTargetProvider{
		agentDefinitionPath: agentPath,
	}

	_, _, err := provider.loadContainerAgentDefinition()
	require.Error(t, err)
	require.Contains(t, err.Error(), "agent.yaml is not valid")
}

func TestShouldUsePreBuiltImage_NoImageDefaultsToBuild(t *testing.T) {
	t.Parallel()

	provider := &AgentServiceTargetProvider{}

	result, err := provider.shouldUsePreBuiltImage(t.Context(), agent_yaml.ContainerAgent{})
	require.NoError(t, err)
	require.False(t, result, "should default to build when no image is configured")
}

func TestShouldUsePreBuiltImage_SelectsPreBuiltImage(t *testing.T) {
	t.Parallel()

	imageURL := "myregistry.azurecr.io/myimage:v1"

	promptStub := &stubPromptServer{selectedIndex: 1}
	client := newPromptTestClient(t, promptStub)

	provider := &AgentServiceTargetProvider{
		azdClient: client,
	}

	result, err := provider.shouldUsePreBuiltImage(t.Context(), agent_yaml.ContainerAgent{Image: imageURL})
	require.NoError(t, err)
	require.True(t, result)
	require.Equal(t, int32(1), promptStub.selectCalls.Load())
	require.NotNil(t, promptStub.lastSelect)
	require.NotNil(t, promptStub.lastSelect.Options)
	require.Len(t, promptStub.lastSelect.Options.Choices, 2)
	require.NotNil(t, promptStub.lastSelect.Options.SelectedIndex)
	require.Equal(t, int32(0), *promptStub.lastSelect.Options.SelectedIndex)
	require.Equal(t, "Build a new image for me", promptStub.lastSelect.Options.Choices[0].Label)
	require.Equal(t, "Create hosted agent from "+imageURL, promptStub.lastSelect.Options.Choices[1].Label)
}

func TestShouldUsePreBuiltImage_SelectsDockerfileBuild(t *testing.T) {
	t.Parallel()

	promptStub := &stubPromptServer{selectedIndex: 0}
	client := newPromptTestClient(t, promptStub)

	provider := &AgentServiceTargetProvider{
		azdClient: client,
	}

	result, err := provider.shouldUsePreBuiltImage(
		t.Context(),
		agent_yaml.ContainerAgent{Image: "myregistry.azurecr.io/myimage:v1"},
	)
	require.NoError(t, err)
	require.False(t, result)
	require.Equal(t, int32(1), promptStub.selectCalls.Load())
}

func TestShouldUsePreBuiltImage_DefaultIndexIsBuild(t *testing.T) {
	t.Parallel()

	// Verify that the default selection index points to "build",
	// so that in --no-prompt mode the framework returns "build" automatically.
	promptStub := &stubPromptServer{selectedIndex: 0}
	client := newPromptTestClient(t, promptStub)

	provider := &AgentServiceTargetProvider{
		azdClient: client,
	}

	result, err := provider.shouldUsePreBuiltImage(
		t.Context(),
		agent_yaml.ContainerAgent{Image: "myregistry.azurecr.io/myimage:v1"},
	)
	require.NoError(t, err)
	require.False(t, result, "default selection (index 0) should mean build from Dockerfile")
	require.NotNil(t, promptStub.lastSelect)
	require.NotNil(t, promptStub.lastSelect.Options.SelectedIndex)
	require.Equal(t, int32(0), *promptStub.lastSelect.Options.SelectedIndex)
	require.Equal(t, "build", promptStub.lastSelect.Options.Choices[0].Value)
}

func TestShouldUsePreBuiltImage_PromptErrorCanRetry(t *testing.T) {
	t.Parallel()

	promptStub := &stubPromptServer{err: fmt.Errorf("prompt failed")}
	client := newPromptTestClient(t, promptStub)

	provider := &AgentServiceTargetProvider{
		azdClient: client,
	}
	agentDef := agent_yaml.ContainerAgent{Image: "myregistry.azurecr.io/myimage:v1"}

	_, err := provider.shouldUsePreBuiltImage(t.Context(), agentDef)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to select hosted agent container image source")

	_, err = provider.shouldUsePreBuiltImage(t.Context(), agentDef)
	require.Error(t, err)
	require.Equal(t, int32(2), promptStub.selectCalls.Load())
}

func TestPackage_SkipsWhenPreBuiltImageChosen(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	imageURL := "myregistry.azurecr.io/myimage:v1"
	agentPath := writeHostedAgentYAMLWithImage(t, dir, imageURL)
	promptStub := &stubPromptServer{selectedIndex: 1}
	client := newPromptTestClient(t, promptStub)

	provider := &AgentServiceTargetProvider{
		azdClient:           client,
		agentDefinitionPath: agentPath,
		env:                 &azdext.Environment{Name: "test-env"},
	}

	var progressMessages []string
	result, err := provider.Package(
		t.Context(),
		&azdext.ServiceConfig{Name: "test-svc"},
		&azdext.ServiceContext{},
		func(msg string) { progressMessages = append(progressMessages, msg) },
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Artifacts, 1)
	require.Equal(t, imageURL, result.Artifacts[0].Location)
	require.Equal(t, azdext.LocationKind_LOCATION_KIND_REMOTE, result.Artifacts[0].LocationKind)
	require.Equal(t, preBuiltImageArtifactSource, result.Artifacts[0].Metadata[preBuiltImageArtifactSourceKey])
	require.Contains(t, progressMessages, "Using pre-built container image, skipping package")
}

func TestPackage_BuildsWhenUserChoseDockerfile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	agentPath := writeHostedAgentYAMLWithImage(t, dir, "myregistry.azurecr.io/myimage:v1")

	containerStub := &stubContainerServer{}
	promptStub := &stubPromptServer{selectedIndex: 0}
	client := newServiceTargetTestClient(t, containerStub, promptStub)

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
	require.NotEmpty(t, result.Artifacts, "expected container artifacts when building from Dockerfile")
	require.Equal(t, int32(1), promptStub.selectCalls.Load())
	require.Equal(t, int32(1), containerStub.buildCalls.Load())
	require.Equal(t, int32(1), containerStub.packageCalls.Load())
}

func TestPublish_SkipsWhenPreBuiltImageChosen(t *testing.T) {
	t.Parallel()

	imageURL := "myregistry.azurecr.io/myimage:v1"

	provider := &AgentServiceTargetProvider{
		env: &azdext.Environment{Name: "test-env"},
	}

	var progressMessages []string
	result, err := provider.Publish(
		t.Context(),
		&azdext.ServiceConfig{Name: "test-svc"},
		&azdext.ServiceContext{Package: []*azdext.Artifact{preBuiltImageArtifact(imageURL)}},
		&azdext.TargetResource{},
		&azdext.PublishOptions{},
		func(msg string) { progressMessages = append(progressMessages, msg) },
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Artifacts, 1)
	require.Equal(t, imageURL, result.Artifacts[0].Location)
	require.Contains(t, progressMessages, "Using pre-built container image, skipping publish")
}

func TestPublish_PublishesWhenPackageBuiltFromDockerfile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	agentPath := writeHostedAgentYAMLWithImage(t, dir, "myregistry.azurecr.io/myimage:v1")
	containerStub := &stubContainerServer{}
	client := newContainerTestClient(t, containerStub)

	provider := &AgentServiceTargetProvider{
		azdClient:           client,
		agentDefinitionPath: agentPath,
		env:                 &azdext.Environment{Name: "test-env"},
	}

	result, err := provider.Publish(
		t.Context(),
		&azdext.ServiceConfig{Name: "test-svc"},
		&azdext.ServiceContext{Package: []*azdext.Artifact{{
			Kind:         azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER,
			Location:     "test-image:latest",
			LocationKind: azdext.LocationKind_LOCATION_KIND_LOCAL,
		}}},
		&azdext.TargetResource{},
		&azdext.PublishOptions{},
		func(string) {},
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.Artifacts, "expected published container artifacts")
	require.Equal(t, int32(1), containerStub.publishCalls.Load())
}

func TestPublish_PrivateACRNetworkAccessGuidance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{
			name: "remote build failed and local fallback unavailable",
			err: errors.New(
				"remote build failed: registry firewall blocked source upload\n\n" +
					"Local fallback unavailable: Docker is not installed",
			),
		},
		{
			name: "acr client ip denied",
			err: errors.New(
				"pushing image: denied: client with IP address '203.0.113.10' " +
					"is not allowed access to registry myregistry.azurecr.io",
			),
		},
		{
			name: "generic host docker login suggestion is overridden",
			err: actionableStatusError(
				t,
				"pushing image to myregistry.azurecr.io failed: Forbidden",
				"When pushing to an external registry, run 'docker login' and try again",
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := publishWithContainerError(t, tt.err)

			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "expected LocalError, got %T: %v", err, err)
			require.Equal(t, exterrors.CodePrivateACRNetworkAccessFailed, localErr.Code)
			require.Equal(t, azdext.LocalErrorCategoryDependency, localErr.Category)
			require.Contains(t, localErr.Message, tt.err.Error())
			require.Contains(t, localErr.Suggestion, "allowlist the public outbound IP/CIDR")
			require.Contains(t, localErr.Suggestion, "docker.remoteBuild: false")
			require.Contains(t, localErr.Suggestion, "Docker or Podman")
			require.NotContains(t, localErr.Suggestion, "docker login")
		})
	}
}

func TestPublish_GenericPublishErrorsAreNotClassifiedAsPrivateACR(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{
			name: "generic publish failure",
			err:  errors.New("registry returned unexpected 500"),
		},
		{
			name: "non-acr denied failure",
			err:  errors.New("denied: requested access to the resource is denied"),
		},
		{
			name: "remote build dockerfile failure without acr network signal",
			err: errors.New(
				"remote build failed: Dockerfile parse error\n\n" +
					"Local fallback unavailable: Docker is not installed",
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := publishWithContainerError(t, tt.err)

			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "expected LocalError, got %T: %v", err, err)
			require.Equal(t, exterrors.OpContainerPublish, localErr.Code)
			require.Equal(t, azdext.LocalErrorCategoryInternal, localErr.Category)
			require.Contains(t, localErr.Message, "container publish failed")
		})
	}
}

func TestPublish_PreservesNonACRHostActionableGuidance(t *testing.T) {
	t.Parallel()

	const suggestion = "run the custom remediation and try again"
	err := publishWithContainerError(t, actionableStatusError(t, "publish failed", suggestion))

	actionable := azdext.ActionableErrorDetailFromError(err)
	require.NotNil(t, actionable)
	require.Equal(t, suggestion, actionable.GetSuggestion())
}

func publishWithContainerError(t *testing.T, publishErr error) error {
	t.Helper()

	dir := t.TempDir()
	agentPath := writeHostedAgentYAMLWithImage(t, dir, "myregistry.azurecr.io/myimage:v1")
	containerStub := &stubContainerServer{publishErr: publishErr}
	client := newContainerTestClient(t, containerStub)

	provider := &AgentServiceTargetProvider{
		azdClient:           client,
		agentDefinitionPath: agentPath,
		env:                 &azdext.Environment{Name: "test-env"},
	}

	_, err := provider.Publish(
		t.Context(),
		&azdext.ServiceConfig{Name: "test-svc"},
		&azdext.ServiceContext{Package: []*azdext.Artifact{{
			Kind:         azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER,
			Location:     "test-image:latest",
			LocationKind: azdext.LocationKind_LOCATION_KIND_LOCAL,
		}}},
		&azdext.TargetResource{},
		&azdext.PublishOptions{},
		func(string) {},
	)
	require.Error(t, err)
	require.Equal(t, int32(1), containerStub.publishCalls.Load())

	return err
}

func actionableStatusError(t *testing.T, message, suggestion string) error {
	t.Helper()

	st := status.New(codes.Unknown, message)
	stWithDetails, err := st.WithDetails(&azdext.ActionableErrorDetail{Suggestion: suggestion})
	require.NoError(t, err)
	return stWithDetails.Err()
}
