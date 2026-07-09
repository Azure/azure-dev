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
	"strings"
	"sync/atomic"
	"testing"

	"azureaiagent/internal/cmd/nextstep"
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

type fakeProjectAgentChecker struct {
	err error
}

func (f fakeProjectAgentChecker) GetAgent(
	context.Context,
	string,
	string,
) (*agent_api.AgentObject, error) {
	if f.err != nil {
		return nil, f.err
	}

	return &agent_api.AgentObject{}, nil
}

func TestWriteExistingAgentVersionWarningIfPresentSkipsErrors(t *testing.T) {
	t.Parallel()

	wroteWarning := writeExistingAgentVersionWarningIfPresent(
		t.Context(),
		fakeProjectAgentChecker{err: errors.New("lookup failed")},
		"test-agent",
	)

	require.False(t, wroteWarning)
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

type stubProjectServer struct {
	azdext.UnimplementedProjectServiceServer
	project *azdext.ProjectConfig
}

func (s *stubProjectServer) Get(
	context.Context, *azdext.EmptyRequest,
) (*azdext.GetProjectResponse, error) {
	return &azdext.GetProjectResponse{Project: s.project}, nil
}

type stubInitializeEnvServer struct {
	azdext.UnimplementedEnvironmentServiceServer
}

func (s *stubInitializeEnvServer) GetCurrent(
	context.Context, *azdext.EmptyRequest,
) (*azdext.EnvironmentResponse, error) {
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: "test-env"}}, nil
}

func (s *stubInitializeEnvServer) GetValue(
	context.Context, *azdext.GetEnvRequest,
) (*azdext.KeyValueResponse, error) {
	return &azdext.KeyValueResponse{Value: "00000000-0000-0000-0000-000000000000"}, nil
}

type stubAccountServer struct {
	azdext.UnimplementedAccountServiceServer
}

func (s *stubAccountServer) LookupTenant(
	context.Context, *azdext.LookupTenantRequest,
) (*azdext.LookupTenantResponse, error) {
	return &azdext.LookupTenantResponse{TenantId: "00000000-0000-0000-0000-000000000000"}, nil
}

func newInitializeTestClient(t *testing.T, projectRoot string) *azdext.AzdClient {
	t.Helper()

	srv := grpc.NewServer()
	azdext.RegisterProjectServiceServer(srv, &stubProjectServer{
		project: &azdext.ProjectConfig{Path: projectRoot},
	})
	azdext.RegisterEnvironmentServiceServer(srv, &stubInitializeEnvServer{})
	azdext.RegisterAccountServiceServer(srv, &stubAccountServer{})

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

func TestInitializeIsCheapAndSideEffectFree(t *testing.T) {
	// azd-core calls ServiceTargetProvider.Initialize for every service on
	// every action (provision, deploy, env refresh, show, ...). Initialize
	// must not touch disk, prompt for credentials, or call Azure. The
	// agent.yaml lookup lives in ensureDeployContext and runs only when
	// a deploy-time entrypoint needs it.

	// Project root with NO agent.yaml/agent.yml anywhere.
	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "svc"), 0o750))

	provider := &AgentServiceTargetProvider{
		azdClient: newInitializeTestClient(t, projectRoot),
	}

	// Initialize must succeed and leave heavy state untouched.
	require.NoError(t, provider.Initialize(t.Context(), &azdext.ServiceConfig{Name: "echo", RelativePath: "svc"}))
	require.Empty(t, provider.agentDefinitionPath)
	require.Nil(t, provider.credential)
	require.Empty(t, provider.tenantId)

	// Same provider, called again with the same service config: still no-op.
	require.NoError(t, provider.Initialize(t.Context(), &azdext.ServiceConfig{Name: "echo", RelativePath: "svc"}))
}

func TestInitializeAcceptsProjectLocalAgentYaml(t *testing.T) {
	t.Setenv("AGENT_DEFINITION_PATH", "")

	projectRoot := t.TempDir()
	serviceDir := filepath.Join(projectRoot, "svc")
	require.NoError(t, os.MkdirAll(serviceDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(serviceDir, "agent.yaml"), []byte("kind: hostedAgent\n"), 0o600))

	provider := &AgentServiceTargetProvider{
		azdClient: newInitializeTestClient(t, projectRoot),
	}

	// Initialize is now cheap: it only stores the service config and does
	// not resolve the agent.yaml on disk. agentDefinitionPath remains
	// empty until a deploy-time entrypoint triggers ensureDeployContext.
	require.NoError(t, provider.Initialize(t.Context(), &azdext.ServiceConfig{Name: "echo", RelativePath: "svc"}))
	require.Empty(t, provider.agentDefinitionPath, "Initialize must not touch disk")

	err := provider.ensureDeployContext(t.Context())

	require.NoError(t, err)
	require.Equal(t, filepath.Join(serviceDir, "agent.yaml"), provider.agentDefinitionPath)
}

func TestInitializeRejectsAgentYamlSymlinkEscapingRoot(t *testing.T) {
	t.Setenv("AGENT_DEFINITION_PATH", "")

	parent := t.TempDir()
	projectRoot := filepath.Join(parent, "project")
	serviceDir := filepath.Join(projectRoot, "svc")
	outside := filepath.Join(parent, "outside")
	require.NoError(t, os.MkdirAll(serviceDir, 0o750))
	require.NoError(t, os.MkdirAll(outside, 0o750))

	outsideAgentYaml := filepath.Join(outside, "agent.yaml")
	require.NoError(t, os.WriteFile(outsideAgentYaml, []byte("kind: hostedAgent\n"), 0o600))
	createSymlinkOrSkip(t, outsideAgentYaml, filepath.Join(serviceDir, "agent.yaml"))

	provider := &AgentServiceTargetProvider{
		azdClient: newInitializeTestClient(t, projectRoot),
	}

	require.NoError(t, provider.Initialize(t.Context(), &azdext.ServiceConfig{Name: "echo", RelativePath: "svc"}))

	err := provider.ensureDeployContext(t.Context())

	require.Error(t, err)
	require.Contains(t, err.Error(), "escapes project root")
	require.Empty(t, provider.agentDefinitionPath)
}

func createSymlinkOrSkip(t *testing.T, oldname, newname string) {
	t.Helper()

	if err := os.Symlink(oldname, newname); err != nil {
		if errors.Is(err, os.ErrPermission) || os.IsPermission(err) ||
			strings.Contains(strings.ToLower(err.Error()), "privilege") {
			t.Skipf("symlink creation not permitted: %v", err)
		}
		t.Fatalf("create symlink: %v", err)
	}
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
		"FOUNDRY_PROJECT_ENDPOINT": "https://proj.azure.com",
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
	require.Equal(
		t,
		"https://proj.azure.com/agents/my-agent/endpoint/protocols/openai/responses?api-version=v1",
		envStub.values["AGENT_MY_SVC_RESPONSES_ENDPOINT"],
	)
	require.Contains(t, envStub.values, "AGENT_MY_SVC_INVOCATIONS_ENDPOINT")
	require.Equal(
		t,
		"https://proj.azure.com/agents/my-agent/endpoint/protocols/invocations?api-version=v1",
		envStub.values["AGENT_MY_SVC_INVOCATIONS_ENDPOINT"],
	)

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
		"FOUNDRY_PROJECT_ENDPOINT": "https://proj.azure.com/",
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
		map[string]string{"FOUNDRY_PROJECT_ENDPOINT": "https://proj.azure.com"},
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
		map[string]string{"FOUNDRY_PROJECT_ENDPOINT": "https://proj.azure.com"},
		&azdext.ServiceConfig{Name: "my-svc"},
		&agent_api.AgentVersionObject{Name: "my-agent", Version: ""},
		nil,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "agent version is empty")
}

func TestDisplayableProtocolFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		protocol         string
		wantNil          bool
		wantProtocol     agent_api.AgentProtocol
		wantEnvSuffix    string
		wantURLContains  string
		wantURLScheme    string // "https" or "wss"
		wantURLOmitAgent bool   // true when the protocol does not embed the agent name in the path
	}{
		{
			name:            "responses",
			protocol:        "responses",
			wantProtocol:    agent_api.AgentProtocolResponses,
			wantEnvSuffix:   "RESPONSES",
			wantURLContains: "/agents/my-agent/endpoint/protocols/openai/responses?api-version=v1",
			wantURLScheme:   "https",
		},
		{
			name:            "invocations",
			protocol:        "invocations",
			wantProtocol:    agent_api.AgentProtocolInvocations,
			wantEnvSuffix:   "INVOCATIONS",
			wantURLContains: "/agents/my-agent/endpoint/protocols/invocations",
			wantURLScheme:   "https",
		},
		{
			name:             "invocations_ws",
			protocol:         "invocations_ws",
			wantProtocol:     agent_api.AgentProtocolInvocationsWS,
			wantEnvSuffix:    "INVOCATIONS_WS",
			wantURLContains:  "/api/projects/agents/endpoint/protocols/invocations_ws",
			wantURLScheme:    "wss",
			wantURLOmitAgent: true,
		},
		{name: "activity excluded", protocol: "activity", wantNil: true},
		{name: "legacy activity_protocol excluded", protocol: "activity_protocol", wantNil: true},
		{name: "unknown excluded", protocol: "unknown_proto", wantNil: true},
	}

	const projectEndpoint = "https://acct.services.ai.azure.com/api/projects/proj"
	const agentName = "my-agent"

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := displayableProtocolFor(tt.protocol)
			if tt.wantNil {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.Equal(t, tt.wantProtocol, got.Protocol)
			require.Equal(t, tt.wantEnvSuffix, got.EnvSuffix)

			// Build URL using the same logic as production code
			eps := agentInvocationEndpoints(projectEndpoint, agentName,
				[]agent_yaml.ProtocolVersionRecord{{Protocol: tt.protocol, Version: "1.0.0"}})
			require.Len(t, eps, 1)
			url := eps[0].URL
			require.True(t, strings.HasPrefix(url, tt.wantURLScheme+"://"),
				"url %q should use %s scheme", url, tt.wantURLScheme)
			require.Contains(t, url, tt.wantURLContains)
			if tt.wantURLOmitAgent {
				require.NotContains(t, url, "/agents/"+agentName+"/")
				require.Contains(t, url, "agent_name="+agentName)
				require.Contains(t, url, "project_name=proj")
			}
		})
	}
}

func TestAgentInvocationEndpoints(t *testing.T) {
	t.Parallel()

	const endpoint = "https://myproject.services.ai.azure.com/api/projects/proj"
	const agentName = "my-agent"
	baseURL := endpoint + "/agents/" + agentName + "/endpoint/protocols/"

	const wsBase = "wss://myproject.services.ai.azure.com" +
		"/api/projects/agents/endpoint/protocols/invocations_ws"
	const wsQuery = "agent_name=" + agentName +
		"&api-version=" + agent_api.AgentEndpointAPIVersion +
		"&project_name=proj"

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
					URL:      baseURL + "openai/responses?api-version=v1",
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
					URL:      baseURL + "invocations?api-version=v1",
				},
			},
		},
		{
			name: "single invocations_ws protocol uses dispatcher form",
			protocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "invocations_ws", Version: "1.0.0"},
			},
			expected: []protocolEndpointInfo{
				{
					Protocol: "invocations_ws",
					URL:      wsBase + "?" + wsQuery,
				},
			},
		},
		{
			name: "multiple protocols with activity_protocol excluded",
			protocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "responses", Version: "1.0.0"},
				{Protocol: "activity_protocol", Version: "1.0.0"},
				{Protocol: "invocations", Version: "1.0.0"},
				{Protocol: "invocations_ws", Version: "1.0.0"},
			},
			expected: []protocolEndpointInfo{
				{
					Protocol: "responses",
					URL:      baseURL + "openai/responses?api-version=v1",
				},
				{
					Protocol: "invocations",
					URL:      baseURL + "invocations?api-version=v1",
				},
				{
					Protocol: "invocations_ws",
					URL:      wsBase + "?" + wsQuery,
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

func TestBuildInvocationsWSProtocolURL_MalformedEndpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		projectEndpoint string
	}{
		{name: "empty", projectEndpoint: ""},
		{name: "missing scheme", projectEndpoint: "myproject.services.ai.azure.com/api/projects/proj"},
		{name: "leading whitespace only", projectEndpoint: "   "},
		{name: "control characters", projectEndpoint: "https://%zz/api/projects/proj"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Empty(t, buildInvocationsWSProtocolURL(tt.projectEndpoint, "my-agent"),
				"expected empty result for malformed projectEndpoint %q", tt.projectEndpoint)
		})
	}
}

func TestBuildInvocationsWSProtocolURL_TrimsWhitespace(t *testing.T) {
	t.Parallel()

	got := buildInvocationsWSProtocolURL(
		"  https://myproject.services.ai.azure.com/api/projects/proj  ",
		"my-agent",
	)
	require.NotEmpty(t, got)
	require.Contains(t, got, "wss://myproject.services.ai.azure.com")
	require.Contains(t, got, "project_name=proj")
	require.Contains(t, got, "agent_name=my-agent")
}

func TestAgentInvocationEndpoints_SkipsInvocationsWSWithMalformedEndpoint(t *testing.T) {
	t.Parallel()

	const malformed = "not-a-url"
	const agentName = "my-agent"

	protocols := []agent_yaml.ProtocolVersionRecord{
		{Protocol: "responses", Version: "1.0.0"},
		{Protocol: "invocations_ws", Version: "1.0.0"},
	}

	got := agentInvocationEndpoints(malformed, agentName, protocols)
	require.Len(t, got, 1, "invocations_ws entry should be filtered when builder returns empty")
	require.Equal(t, "responses", got[0].Protocol)
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
		"/agents/test-agent/endpoint/protocols/openai/responses?api-version=v1"
	require.Equal(t, wantResponses, artifacts[0].Location)
	require.Equal(t, "Agent endpoint (responses)", artifacts[0].Metadata["label"])
	require.Empty(t, artifacts[0].Metadata["note"],
		"note should only appear on the last endpoint")

	wantInvocations := ep +
		"/agents/test-agent/endpoint/protocols/invocations" +
		"?api-version=v1"
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
		"/agents/prompt-agent/endpoint/protocols/openai/responses?api-version=v1"
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

func TestLoadContainerAgentDefinition_EnvPathOverridesInlineDefinition(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	agentPath := filepath.Join(dir, "agent.yaml")
	require.NoError(t, os.WriteFile(
		agentPath,
		[]byte("kind: hosted\nname: override-agent\nprotocols:\n  - protocol: responses\n    version: \"1.0.0\"\n"),
		0o600,
	))

	props, err := AgentDefinitionToServiceProperties(sampleContainerAgent(), nil)
	require.NoError(t, err)
	provider := &AgentServiceTargetProvider{
		agentDefinitionPath: agentPath,
		serviceConfig: &azdext.ServiceConfig{
			Name:                 "basic-agent",
			Host:                 "azure.ai.agent",
			AdditionalProperties: props,
		},
	}

	got, isHosted, err := provider.loadContainerAgentDefinition()
	require.NoError(t, err)
	require.True(t, isHosted)
	require.Equal(t, "override-agent", got.Name)
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
			name: "remote build failed and local fallback unavailable with acr context",
			err: errors.New(
				"remote build failed: myregistry.azurecr.io firewall blocked source upload\n\n" +
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
			name: "private endpoint without public access on ARM call",
			err: errors.New(
				"POST https://management.azure.com/.../Microsoft.ContainerRegistry/registries/myregistry/" +
					"listBuildSourceUploadUrl: private endpoint required; public network access disabled",
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

// initTest15RemoteBuildRBACError is the wire-format error captured on
// 2026-05-29 from a repro on the init-test-15 project when running
// `azd deploy` as a service principal with Reader-only access. It exercises
// the new default container deploy path (docker.remoteBuild: true) hitting
// ARM's listBuildSourceUploadUrl with no ACR push role. The error is
// preserved verbatim so future refactors cannot silently re-introduce the
// pre-2026-05 misclassification ("Container Registry may be blocking network
// access") on what is actually an RBAC failure.
const initTest15RemoteBuildRBACError = "rpc error: code = Unknown desc = remote build failed: " +
	"POST https://management.azure.com/subscriptions/5f416acb-98a5-411a-808e-f37c0fbbbdb5/" +
	"resourceGroups/rg-init-test-15-dev/providers/Microsoft.ContainerRegistry/" +
	"registries/crpjhtjmfdtwcau/listBuildSourceUploadUrl\n" +
	"--------------------------------------------------------------------------------\n" +
	"RESPONSE 403: 403 Forbidden\n" +
	"ERROR CODE: AuthorizationFailed\n" +
	"--------------------------------------------------------------------------------\n" +
	"{\n" +
	"  \"error\": {\n" +
	"    \"code\": \"AuthorizationFailed\",\n" +
	"    \"message\": \"The client 'cdd73b03-a291-42d1-8fd5-903957338f08' with object id " +
	"'209256b0-0f0c-41f4-a7e2-bceaba1ca711' does not have authorization to perform action " +
	"'Microsoft.ContainerRegistry/registries/listBuildSourceUploadUrl/action' over scope " +
	"'/subscriptions/5f416acb-98a5-411a-808e-f37c0fbbbdb5/resourceGroups/rg-init-test-15-dev/" +
	"providers/Microsoft.ContainerRegistry/registries/crpjhtjmfdtwcau' or the scope is invalid. " +
	"If access was recently granted, please refresh your credentials.\"\n" +
	"  }\n" +
	"}\n" +
	"--------------------------------------------------------------------------------\n\n" +
	"Local fallback unavailable: the docker service is not running, please start it: exit code: 1"

func TestPublish_ACRPermissionGuidance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		err                 error
		expectedPrimaryRole string
		expectedRoleID      string
		expectedPathContext string
	}{
		{
			name:                "init-test-15 remote build RBAC fixture",
			err:                 errors.New(initTest15RemoteBuildRBACError),
			expectedPrimaryRole: "Container Registry Tasks Contributor",
			expectedRoleID:      roleContainerRegistryTasksContributor,
			expectedPathContext: "ACR Tasks remote build",
		},
		{
			name: "docker push denied requested access",
			err: errors.New(
				"failed to push image to myregistry.azurecr.io/app:v1: " +
					"denied: requested access to the resource is denied",
			),
			expectedPrimaryRole: "AcrPush",
			expectedRoleID:      roleAcrPush,
			expectedPathContext: "data-plane push",
		},
		{
			name: "docker push 401 unauthorized authentication required",
			err: errors.New(
				"pushing to myregistry.azurecr.io: 401 Unauthorized: authentication required",
			),
			expectedPrimaryRole: "AcrPush",
			expectedRoleID:      roleAcrPush,
			expectedPathContext: "data-plane push",
		},
		{
			name: "acr token exchange failure",
			err: errors.New(
				"failed to fetch oauth token for myregistry.azurecr.io: insufficient_scope",
			),
			expectedPrimaryRole: "AcrPush",
			expectedRoleID:      roleAcrPush,
			expectedPathContext: "data-plane push",
		},
		{
			name: "actionable docker login suggestion is overridden",
			err: actionableStatusError(
				t,
				"pushing image to myregistry.azurecr.io failed: 403 Forbidden",
				"When pushing to an external registry, run 'docker login' and try again",
			),
			expectedPrimaryRole: "AcrPush",
			expectedRoleID:      roleAcrPush,
			expectedPathContext: "data-plane push",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := publishWithContainerError(t, tt.err)

			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "expected LocalError, got %T: %v", err, err)
			require.Equal(t, exterrors.CodeACRPermissionDenied, localErr.Code,
				"should classify as permission denied, not %q", localErr.Code)
			require.Equal(t, azdext.LocalErrorCategoryDependency, localErr.Category)
			require.Contains(t, localErr.Message,
				"does not have permission to push")
			require.Contains(t, localErr.Suggestion, tt.expectedPathContext,
				"suggestion should identify the failing path")
			require.Contains(t, localErr.Suggestion, tt.expectedPrimaryRole,
				"suggestion prose should name the role")
			require.Contains(t, localErr.Suggestion, tt.expectedRoleID,
				"suggestion should include the role ID alongside the name")
			require.Contains(t, localErr.Suggestion,
				fmt.Sprintf(`--role %s`, tt.expectedRoleID),
				"az command should use the role GUID for stability")
			require.NotContains(t, localErr.Suggestion,
				fmt.Sprintf(`--role "%s"`, tt.expectedPrimaryRole),
				"az command should not use the display name -- use the GUID instead")
			require.Contains(t, localErr.Suggestion, "AZD_AGENT_SKIP_ACR")
			require.Contains(t, localErr.Suggestion, "code_configuration")
			require.Contains(t, localErr.Suggestion, "azd up")
			require.NotContains(t, localErr.Suggestion, "docker login")
			require.NotContains(t, localErr.Suggestion, "allowlist the public outbound IP/CIDR")
		})
	}
}

// TestPublish_ACRPermissionGuidance_DynamicSubstitution verifies that when the
// underlying ARM error includes the principal object id and ACR resource scope
// (typical of remoteBuild=true RBAC failures), those values are substituted
// into the example `az role assignment create` command so the user can copy
// and paste it directly. Also confirms the remote-build path triggers the
// "Container Registry Tasks Contributor" recommendation (via GUID), not
// "AcrPush" (AcrPush is data-plane only and does NOT grant
// listBuildSourceUploadUrl).
func TestPublish_ACRPermissionGuidance_DynamicSubstitution(t *testing.T) {
	t.Parallel()

	err := publishWithContainerError(t, errors.New(initTest15RemoteBuildRBACError))

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected LocalError, got %T: %v", err, err)
	require.Equal(t, exterrors.CodeACRPermissionDenied, localErr.Code)

	// Both identifiers from the captured fixture must appear inline in the
	// command -- not as placeholders -- so the user can paste it as-is.
	// The role is identified by its definition GUID, not its display name,
	// so the command remains valid even if Azure ever renames the role.
	expectedCmd := fmt.Sprintf(
		`az role assignment create `+
			`--assignee 209256b0-0f0c-41f4-a7e2-bceaba1ca711 `+
			`--role %s `+
			`--scope /subscriptions/5f416acb-98a5-411a-808e-f37c0fbbbdb5/`+
			`resourceGroups/rg-init-test-15-dev/providers/Microsoft.ContainerRegistry/`+
			`registries/crpjhtjmfdtwcau`,
		roleContainerRegistryTasksContributor,
	)
	require.Contains(t, localErr.Suggestion, expectedCmd)
	require.NotContains(t, localErr.Suggestion, "<your-object-id>")
	require.NotContains(t, localErr.Suggestion, "<acr-resource-id>")
}

// TestPublish_ACRPermissionGuidance_PlaceholderFallback verifies that when the
// error shape lacks an object id and/or ACR scope (e.g. docker-push errors
// from the local-build path), the suggestion gracefully falls back to
// placeholder tokens rather than emitting a broken command. The local-push
// path also gets the AcrPush GUID recommendation, not Tasks Contributor.
func TestPublish_ACRPermissionGuidance_PlaceholderFallback(t *testing.T) {
	t.Parallel()

	err := publishWithContainerError(t, errors.New(
		"failed to push image to myregistry.azurecr.io/app:v1: "+
			"denied: requested access to the resource is denied",
	))

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected LocalError, got %T: %v", err, err)
	require.Equal(t, exterrors.CodeACRPermissionDenied, localErr.Code)
	require.Contains(t, localErr.Suggestion, "<your-object-id>")
	require.Contains(t, localErr.Suggestion, "<acr-resource-id>")
	require.Contains(t, localErr.Suggestion, fmt.Sprintf(
		`az role assignment create --assignee <your-object-id> --role %s --scope <acr-resource-id>`,
		roleAcrPush,
	))
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
			name: "non-acr 403 forbidden",
			err:  errors.New("403 forbidden from foundry control plane"),
		},
		{
			name: "remote build dockerfile failure without acr context",
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
			require.Equal(t, exterrors.OpContainerPublish, localErr.Code,
				"should fall through to internal, not %q", localErr.Code)
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

func TestAugmentDeployNote_NoReadme_AppendsBelowAkaMsLink(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	// No README written; readmeExists closure should return false.

	state := &nextstep.State{
		Services: []nextstep.ServiceState{
			{
				Name:         "echo",
				RelativePath: "src/echo",
				Protocol:     "invocations",
				IsDeployed:   true,
			},
		},
	}

	artifact := &azdext.Artifact{
		Kind: azdext.ArtifactKind_ARTIFACT_KIND_ENDPOINT,
		Metadata: map[string]string{
			"label": "Agent endpoint (invocations)",
			"note":  "static aka.ms link",
		},
	}

	augmentDeployNote(state, []*azdext.Artifact{artifact}, tmp, "" /* no configDir → cache lookup is a no-op */)

	got := artifact.Metadata["note"]
	require.Contains(t, got, "static aka.ms link", "aka.ms link should be preserved when no README is present")
	require.Contains(t, got, "Next:", "Next: block should be appended")
	require.Contains(t, got, "azd ai agent invoke ", "should suggest invoking the deployed agent")
	require.Equal(t, 1, strings.Count(got, "Next:"), "Next: header should appear exactly once")
}

func TestAugmentDeployNote_WithReadme_ReplacesAkaMsLink(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	servicePath := filepath.Join(tmp, "src", "echo")
	require.NoError(t, os.MkdirAll(servicePath, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(servicePath, "README.md"), []byte("sample"), 0o600))

	state := &nextstep.State{
		Services: []nextstep.ServiceState{
			{
				Name:         "echo",
				RelativePath: "src/echo",
				Protocol:     "invocations",
				IsDeployed:   true,
			},
		},
	}

	artifact := &azdext.Artifact{
		Kind: azdext.ArtifactKind_ARTIFACT_KIND_ENDPOINT,
		Metadata: map[string]string{
			"label": "Agent endpoint (invocations)",
			"note":  "static aka.ms link",
		},
	}

	augmentDeployNote(state, []*azdext.Artifact{artifact}, tmp, "")

	got := artifact.Metadata["note"]
	require.NotContains(t, got, "static aka.ms link",
		"aka.ms line must be replaced when a local README provides richer guidance")
	require.Contains(t, got, "Next:", "Next: block should be present")
	require.Contains(t, got, "see src/echo/README.md", "README pointer should be present")
}

func TestAugmentDeployNote_WithRootReadme_ReplacesAkaMsLink(t *testing.T) {
	t.Parallel()

	for _, rel := range []string{"", "."} {
		t.Run(fmt.Sprintf("rel=%q", rel), func(t *testing.T) {
			t.Parallel()

			tmp := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(tmp, "README.md"), []byte("sample"), 0o600))

			state := &nextstep.State{
				Services: []nextstep.ServiceState{
					{
						Name:         "echo",
						RelativePath: rel,
						Protocol:     "invocations",
						IsDeployed:   true,
					},
				},
			}

			artifact := &azdext.Artifact{
				Kind: azdext.ArtifactKind_ARTIFACT_KIND_ENDPOINT,
				Metadata: map[string]string{
					"label": "Agent endpoint (invocations)",
					"note":  "static aka.ms link",
				},
			}

			augmentDeployNote(state, []*azdext.Artifact{artifact}, tmp, "")

			got := artifact.Metadata["note"]
			require.NotContains(t, got, "static aka.ms link",
				"aka.ms line must be replaced when a local README provides richer guidance")
			require.Contains(t, got, "see README.md", "README pointer should be present")
		})
	}
}

func TestAugmentDeployNote_ReadmeTraversalDoesNotReplaceAkaMsLink(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	projectRoot := filepath.Join(parent, "project")
	outside := filepath.Join(parent, "outside")
	require.NoError(t, os.MkdirAll(projectRoot, 0o750))
	require.NoError(t, os.MkdirAll(outside, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "README.md"), []byte("outside"), 0o600))

	state := &nextstep.State{
		Services: []nextstep.ServiceState{
			{
				Name:         "echo",
				RelativePath: "../outside",
				Protocol:     "invocations",
				IsDeployed:   true,
			},
		},
	}

	artifact := &azdext.Artifact{
		Kind: azdext.ArtifactKind_ARTIFACT_KIND_ENDPOINT,
		Metadata: map[string]string{
			"label": "Agent endpoint (invocations)",
			"note":  "static aka.ms link",
		},
	}

	augmentDeployNote(state, []*azdext.Artifact{artifact}, projectRoot, "")

	got := artifact.Metadata["note"]
	require.Contains(t, got, "static aka.ms link")
	require.Contains(t, got, "Next:")
}

func TestAugmentDeployNote_CachedSpecYieldsPayloadOverride(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	configDir := filepath.Join(tmp, ".azure", "dev")
	require.NoError(t, os.MkdirAll(configDir, 0o750))
	// ReadCachedOpenAPISpec / sanitizeAgentName: the filename uses the agent
	// name verbatim when it contains only safe characters.
	spec := `{
  "paths": {
    "/invocations": {
      "post": {
        "requestBody": {
          "content": {
            "application/json": {
              "example": {"prompt": "from cache"}
            }
          }
        }
      }
    }
  }
}`
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "openapi-echo-local.json"), []byte(spec), 0o600))

	state := &nextstep.State{
		Services: []nextstep.ServiceState{
			{
				Name:         "echo",
				RelativePath: "src/echo",
				Protocol:     "invocations",
				IsDeployed:   true,
			},
		},
	}

	artifact := &azdext.Artifact{
		Kind: azdext.ArtifactKind_ARTIFACT_KIND_ENDPOINT,
		Metadata: map[string]string{
			"label": "Agent endpoint (invocations)",
			"note":  "static aka.ms link",
		},
	}

	augmentDeployNote(state, []*azdext.Artifact{artifact}, tmp, configDir)

	got := artifact.Metadata["note"]
	require.Contains(t, got, `"prompt":"from cache"`,
		"cached OpenAPI example should drive the suggested invoke payload")
}

func TestAugmentDeployNote_NoteAttachedToLastEndpoint(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()

	state := &nextstep.State{
		Services: []nextstep.ServiceState{
			{
				Name:         "echo",
				RelativePath: "src/echo",
				Protocol:     "invocations",
				IsDeployed:   true,
			},
		},
	}

	playground := &azdext.Artifact{
		Kind:     azdext.ArtifactKind_ARTIFACT_KIND_ENDPOINT,
		Metadata: map[string]string{"label": "Agent playground (portal)"},
	}
	first := &azdext.Artifact{
		Kind:     azdext.ArtifactKind_ARTIFACT_KIND_ENDPOINT,
		Metadata: map[string]string{"label": "Agent endpoint (responses)"},
	}
	last := &azdext.Artifact{
		Kind: azdext.ArtifactKind_ARTIFACT_KIND_ENDPOINT,
		Metadata: map[string]string{
			"label": "Agent endpoint (invocations)",
			"note":  "static aka.ms link",
		},
	}

	augmentDeployNote(state, []*azdext.Artifact{playground, first, last}, tmp, "")

	require.NotContains(t, playground.Metadata["note"], "Next:", "playground artifact must remain untouched")
	require.NotContains(t, first.Metadata["note"], "Next:", "non-note endpoint must remain untouched")
	require.Contains(t, last.Metadata["note"], "Next:", "augmentation must target the last note-bearing artifact")
}

func TestAugmentDeployNote_NilStateIsNoOp(t *testing.T) {
	t.Parallel()

	artifact := &azdext.Artifact{
		Kind: azdext.ArtifactKind_ARTIFACT_KIND_ENDPOINT,
		Metadata: map[string]string{
			"label": "Agent endpoint (invocations)",
			"note":  "static aka.ms link",
		},
	}
	augmentDeployNote(nil, []*azdext.Artifact{artifact}, "/tmp", "")
	require.Equal(t, "static aka.ms link", artifact.Metadata["note"], "nil state must leave the static note intact")
}

func TestAugmentDeployNote_NoNoteBearingArtifactIsNoOp(t *testing.T) {
	t.Parallel()

	state := &nextstep.State{
		Services: []nextstep.ServiceState{
			{Name: "echo", RelativePath: "src/echo", Protocol: "invocations", IsDeployed: true},
		},
	}
	playground := &azdext.Artifact{
		Kind:     azdext.ArtifactKind_ARTIFACT_KIND_ENDPOINT,
		Metadata: map[string]string{"label": "Agent playground (portal)"},
	}
	augmentDeployNote(state, []*azdext.Artifact{playground}, "/tmp", "")
	require.Empty(t, playground.Metadata["note"], "no note-bearing artifact → nothing to augment")
}

// TestAugmentDeployNote_NoServicesIsNoOp covers a partial-state branch:
// ResolveAfterDeploy short-circuits on len(state.Services) == 0, so the
// existing static note must survive unchanged.
func TestAugmentDeployNote_NoServicesIsNoOp(t *testing.T) {
	t.Parallel()

	artifact := &azdext.Artifact{
		Kind: azdext.ArtifactKind_ARTIFACT_KIND_ENDPOINT,
		Metadata: map[string]string{
			"label": "Agent endpoint (invocations)",
			"note":  "static aka.ms link",
		},
	}
	augmentDeployNote(&nextstep.State{}, []*azdext.Artifact{artifact}, "/tmp", "")
	require.Equal(t, "static aka.ms link", artifact.Metadata["note"])
}

// TestAugmentDeployNote_LowercaseReadme_DoesNotReplaceFallback locks the
// casing-mismatch guard: when only a lowercase readme.md exists on a
// case-sensitive filesystem, the resolver would still emit a literal
// "README.md" pointer that does not resolve on disk  and the aka.ms
// fallback would be lost. The fix tightens readmeExists to the canonical
// casing so the append branch fires and the static link is preserved.
func TestAugmentDeployNote_LowercaseReadme_DoesNotReplaceFallback(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	// Detect case-sensitivity at runtime; the fix is meaningful only on
	// case-sensitive filesystems (Linux, WSL). On Windows NTFS and default
	// macOS APFS the OS resolves "README.md" → "readme.md" transparently,
	// which would make readmeExists return true even after the fix.
	probe := filepath.Join(tmp, "case-probe.txt")
	require.NoError(t, os.WriteFile(probe, nil, 0o600))
	if _, err := os.Stat(filepath.Join(tmp, "CASE-PROBE.TXT")); err == nil {
		t.Skip("case-insensitive filesystem — readmeExists casing guard is a no-op here")
	}

	servicePath := filepath.Join(tmp, "src", "echo")
	require.NoError(t, os.MkdirAll(servicePath, 0o750))
	// Only lowercase readme.md exists; canonical README.md does not.
	require.NoError(t, os.WriteFile(filepath.Join(servicePath, "readme.md"), []byte("sample"), 0o600))

	state := &nextstep.State{
		Services: []nextstep.ServiceState{
			{
				Name:         "echo",
				RelativePath: "src/echo",
				Protocol:     "invocations",
				IsDeployed:   true,
			},
		},
	}

	artifact := &azdext.Artifact{
		Kind: azdext.ArtifactKind_ARTIFACT_KIND_ENDPOINT,
		Metadata: map[string]string{
			"label": "Agent endpoint (invocations)",
			"note":  "static aka.ms link",
		},
	}

	augmentDeployNote(state, []*azdext.Artifact{artifact}, tmp, "")

	got := artifact.Metadata["note"]
	require.Contains(t, got, "static aka.ms link",
		"aka.ms fallback must survive when only lowercase readme.md exists on disk")
	require.NotContains(t, got, "see src/echo/README.md",
		"resolver must not emit a README pointer that does not match what is on disk")
}

// TestAugmentDeployNote_MultiServiceState_ScopedToDeployedService locks
// the deploy-hook contract that the rendered Next: block reflects only
// the service whose artifact note is being augmented. The hook applies
// filterServicesByName to the assembled state before invoking the
// resolver.
func TestAugmentDeployNote_MultiServiceState_ScopedToDeployedService(t *testing.T) {
	t.Parallel()

	state := &nextstep.State{
		Services: []nextstep.ServiceState{
			{Name: "alpha", RelativePath: "src/alpha", Protocol: "invocations", IsDeployed: true},
			{Name: "beta", RelativePath: "src/beta", Protocol: "invocations", IsDeployed: true},
		},
	}
	state.Services = filterServicesByName(state.Services, "alpha")

	artifact := &azdext.Artifact{
		Kind: azdext.ArtifactKind_ARTIFACT_KIND_ENDPOINT,
		Metadata: map[string]string{
			"label": "Agent endpoint (invocations)",
			"note":  "static aka.ms link",
		},
	}

	augmentDeployNote(state, []*azdext.Artifact{artifact}, "/tmp", "")

	got := artifact.Metadata["note"]
	require.NotContains(t, got, "beta",
		"other-service guidance must not leak into the deployed service's note")
	require.Contains(t, got, "Next:", "Next: block should be present for the deployed service")
}

// TestFilterServicesByName covers the helper used at the deploy-hook call site.
func TestFilterServicesByName(t *testing.T) {
	t.Parallel()

	services := []nextstep.ServiceState{
		{Name: "alpha"},
		{Name: "beta"},
		{Name: "gamma"},
	}

	require.Equal(t, []nextstep.ServiceState{{Name: "beta"}}, filterServicesByName(services, "beta"),
		"match returns single-element slice")
	require.Nil(t, filterServicesByName(services, "missing"),
		"no match returns nil  caller short-circuits on empty Services")
	require.Equal(t, services, filterServicesByName(services, ""),
		"empty name returns input unchanged (defensive)")
}

func TestValidatePythonBundledDeps_NoRequirements(t *testing.T) {
	dir := t.TempDir()
	// No requirements.txt — should pass
	err := validatePythonBundledDeps(dir)
	require.NoError(t, err)
}

func TestValidatePythonBundledDeps_EmptyRequirements(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("# just a comment\n\n"), 0600))

	err := validatePythonBundledDeps(dir)
	require.NoError(t, err)
}

func TestValidatePythonBundledDeps_NoDepsInstalled(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("azure-ai-agents>=1.0\n"), 0600))

	err := validatePythonBundledDeps(dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no installed packages were found")
}

func TestValidatePythonBundledDeps_TopLevelDistInfo(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("azure-ai-agents>=1.0\n"), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "azure_ai_agents-1.0.dist-info"), 0o750))

	err := validatePythonBundledDeps(dir)
	require.NoError(t, err)
}

func TestValidatePythonBundledDeps_SubdirDistInfo(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("azure-ai-agents>=1.0\n"), 0600))
	// Installed into vendor/ subdir
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "vendor", "azure_ai_agents-1.0.dist-info"), 0o750))

	err := validatePythonBundledDeps(dir)
	require.NoError(t, err)
}

func TestValidatePythonBundledDeps_ErrorCodeCorrect(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("some-package\n"), 0600))

	err := validatePythonBundledDeps(dir)
	require.Error(t, err)

	var localErr *azdext.LocalError
	require.True(t, errors.As(err, &localErr))
	require.Equal(t, exterrors.CodeBundledDepsNotFound, localErr.Code)
}
