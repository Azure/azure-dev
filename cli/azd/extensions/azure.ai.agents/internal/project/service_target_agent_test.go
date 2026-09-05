// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/botservice"
	"azureaiagent/internal/pkg/envkey"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestVoiceAgentInlineServicePropertiesRoundTrip_BYOM(t *testing.T) {
	instructions := "Route callers to the right team."
	voice := "alloy"
	store := true
	props, err := VoiceAgentDefinitionToServiceProperties(agent_yaml.VoiceAgent{
		AgentDefinition: agent_yaml.AgentDefinition{
			Kind: agent_yaml.AgentKindPromptVoice,
			Name: "voice-agent",
		},
		ModelType:    agent_yaml.VoiceModelTypeSelfDeployed,
		Model:        &agent_yaml.Model{Id: "my-realtime-deployment"},
		Instructions: &instructions,
		Voice:        &voice,
		Store:        &store,
		Telephony: &agent_yaml.VoiceTelephony{Bindings: []agent_yaml.VoiceTelephonyBinding{
			{Provider: "twilio", Identifier: "+14255550123", Connection: "telephony-twilio"},
		}},
	}, nil)
	require.NoError(t, err)

	svc := &azdext.ServiceConfig{
		Name:                 "voice-agent",
		Host:                 "azure.ai.agent",
		AdditionalProperties: props,
	}
	got, found, err := VoiceAgentFromResolvedService(svc, t.TempDir())
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, agent_yaml.AgentKindPromptVoice, got.Kind)
	require.Equal(t, "voice-agent", got.Name)
	require.Equal(t, agent_yaml.VoiceModelTypeSelfDeployed, got.ModelType)
	require.NotNil(t, got.Model)
	require.Equal(t, "my-realtime-deployment", got.Model.Id)
	require.NotNil(t, got.Instructions)
	require.Equal(t, instructions, *got.Instructions)
	require.NotNil(t, got.Voice)
	require.Equal(t, voice, *got.Voice)
	require.NotNil(t, got.Store)
	require.Equal(t, store, *got.Store)
	require.NotNil(t, got.Telephony)
	require.Len(t, got.Telephony.Bindings, 1)
	require.Equal(t, "twilio", got.Telephony.Bindings[0].Provider)
	require.Equal(t, "+14255550123", got.Telephony.Bindings[0].Identifier)
	require.Equal(t, "telephony-twilio", got.Telephony.Bindings[0].Connection)
}

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

func TestServiceHasTelephony(t *testing.T) {
	props := &structpb.Struct{Fields: map[string]*structpb.Value{
		"telephony": structpb.NewStructValue(&structpb.Struct{}),
	}}
	require.True(t, serviceHasTelephony(&azdext.ServiceConfig{AdditionalProperties: props}))
	require.False(t, serviceHasTelephony(&azdext.ServiceConfig{}))
}

func TestShouldRejectTelephonyForNonVoice(t *testing.T) {
	props := &structpb.Struct{Fields: map[string]*structpb.Value{
		"telephony": structpb.NewStructValue(&structpb.Struct{}),
	}}
	svc := &azdext.ServiceConfig{AdditionalProperties: props}

	require.False(t, shouldRejectTelephonyForNonVoice(true, false, svc))
	require.False(t, shouldRejectTelephonyForNonVoice(false, true, svc))
	require.True(t, shouldRejectTelephonyForNonVoice(false, false, svc))
}

func TestTelephonyBindingMatches(t *testing.T) {
	desired := &agent_api.TelephonyBindingRequest{
		Provider:        "twilio",
		Identifier:      "+14255550123",
		ConnectionName:  "telephony-twilio",
		TransferTargets: []map[string]any{{"kind": "phone", "target": "+14255550124"}},
	}
	remote := &agent_api.TelephonyBinding{
		ID:              "twilio:%2B14255550123",
		Provider:        "twilio",
		Identifier:      "+14255550123",
		ConnectionName:  "telephony-twilio",
		TransferTargets: []map[string]any{{"kind": "phone", "target": "+14255550124"}},
	}
	require.True(t, telephonyBindingMatches(remote, desired))
	remote.ConnectionName = "other"
	require.False(t, telephonyBindingMatches(remote, desired))
	remote.ConnectionName = "telephony-twilio"
	remote.TransferTargets = []map[string]any{}
	desired.TransferTargets = nil
	require.True(t, telephonyBindingMatches(remote, desired))
}

func TestTelephonyBindingMatches_ServiceOmittedFields(t *testing.T) {
	// ACS binding responses can omit request fields while still returning the stable binding id.
	desired := &agent_api.TelephonyBindingRequest{
		Provider:       "azure-communication-service",
		Identifier:     "28:orgid:00000000-0000-0000-0000-000000000001",
		ConnectionName: "telephony-acs",
	}
	remote := &agent_api.TelephonyBinding{
		ID:         "azure-communication-service:28:orgid:00000000-0000-0000-0000-000000000001",
		Provider:   "teams_phone_extension",
		Connection: "telephony-acs",
		Status:     "active",
	}
	require.True(t, telephonyBindingMatches(remote, desired))

	remote.ID = "azure-communication-service:28:orgid:00000000-0000-0000-0000-000000000002"
	require.False(t, telephonyBindingMatches(remote, desired))
}

func TestTelephonyWireProvider(t *testing.T) {
	require.Equal(t, "azure-communication-service", telephonyWireProvider("acs"))
	require.Equal(t, "twilio", telephonyWireProvider("twilio"))
}

type fakeProjectAgentChecker struct {
	err error
}

func (f fakeProjectAgentChecker) GetAgent(
	context.Context,
	string,
	string,
	bool,
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

func TestAgentDeploymentFailedErrorPreservesServiceDetails(t *testing.T) {
	t.Parallel()

	err := agentDeploymentFailedError(&agent_api.AgentVersionObject{
		Error: &agent_api.AgentVersionError{
			Code:    string(nextstep.UserErrorImage),
			Message: "the image could not be started",
		},
		RequestID: "request-id",
	}, "sample.services.ai.azure.com")

	serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
	require.True(t, ok)
	require.Equal(t, "create_agent.ImageError", serviceErr.ErrorCode)
	require.Equal(t, "sample.services.ai.azure.com", serviceErr.ServiceName)
	require.Contains(t, serviceErr.Message, "[ImageError] the image could not be started")
	require.Contains(t, serviceErr.Message, "request-id")
	require.Contains(t, serviceErr.Suggestion, "azd ai agent monitor --type system --follow")
}

func TestAgentDeploymentFailedErrorUsesFallbackCode(t *testing.T) {
	t.Parallel()

	err := agentDeploymentFailedError(&agent_api.AgentVersionObject{}, "sample.services.ai.azure.com")

	serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
	require.True(t, ok)
	require.Equal(t, "create_agent.failed", serviceErr.ErrorCode)
	require.Equal(
		t,
		"run `azd ai agent show` to inspect the latest deployment status",
		serviceErr.Suggestion,
	)
}

func TestClassifyActivityBotErrorUsesMsaAppIDConflictCode(t *testing.T) {
	t.Parallel()

	err := classifyActivityBotError(errors.New("MsaAppId is already in use"), "client-id-1")

	serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
	require.True(t, ok)
	require.Equal(t, "ensure_activity_bot.msa_app_id_already_in_use", serviceErr.ErrorCode)
	require.Equal(t, "botservice", serviceErr.ServiceName)
	require.Equal(
		t,
		"configure the Activity Bot name to use the existing Azure Bot bound to this MsaAppID, "+
			"or remove that Bot, then retry",
		serviceErr.Suggestion,
	)
}

func TestResolveActivityBotNameReturnsMultipleBotClassification(t *testing.T) {
	t.Parallel()

	p := &AgentServiceTargetProvider{}
	_, _, err := p.resolveActivityBotName(
		t.Context(),
		fakeActivityBotFinder{err: &botservice.MultipleBotsForMsaAppIDError{}},
		"my-svc",
		"agent-a",
		"client-id-1",
		"fallback-rg",
		map[string]string{envkey.AgentBotName("my-svc"): "env-bot"},
	)

	serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
	require.True(t, ok)
	require.Equal(t, "get_activity_bot.multiple_bots_for_msa_app_id", serviceErr.ErrorCode)
	require.Empty(t, serviceErr.ServiceName)
}

func TestClassifyActivityBotErrorSeparatesTeamsChannelFailures(t *testing.T) {
	t.Parallel()

	responseErr := &azcore.ResponseError{ErrorCode: "AuthorizationFailed", StatusCode: 403}
	err := classifyActivityBotError(&botservice.TeamsChannelError{Err: responseErr}, "client-id-1")

	serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
	require.True(t, ok)
	require.Equal(t, "ensure_teams_channel.AuthorizationFailed", serviceErr.ErrorCode)
}

func TestClassifyActivityBotErrorPreservesBotServiceFailures(t *testing.T) {
	t.Parallel()

	responseErr := &azcore.ResponseError{ErrorCode: "InvalidBotConfiguration", StatusCode: 400}
	err := classifyActivityBotError(responseErr, "client-id-1")

	serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
	require.True(t, ok)
	require.Equal(t, "ensure_activity_bot.InvalidBotConfiguration", serviceErr.ErrorCode)
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

func TestDependencyConditionLookupPrefersAzdEnvironment(t *testing.T) {
	t.Setenv("DEPLOY_TOOLS", "true")
	provider := &AgentServiceTargetProvider{dependencyEnv: map[string]string{"DEPLOY_TOOLS": "false"}}
	require.Equal(t, "false", provider.dependencyEnvValue("DEPLOY_TOOLS"))
	provider.dependencyEnv = nil
	require.Equal(t, "true", provider.dependencyEnvValue("DEPLOY_TOOLS"))
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
	buildRequest *azdext.ContainerBuildRequest
	packRequest  *azdext.ContainerPackageRequest
	pubRequest   *azdext.ContainerPublishRequest
	packageImage string
	publishImage string
	publishErr   error
}

func (s *stubContainerServer) Build(
	_ context.Context,
	request *azdext.ContainerBuildRequest,
) (*azdext.ContainerBuildResponse, error) {
	s.buildCalls.Add(1)
	s.buildRequest = request
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
	request *azdext.ContainerPackageRequest,
) (*azdext.ContainerPackageResponse, error) {
	s.packageCalls.Add(1)
	s.packRequest = request
	image := s.packageImage
	if image == "" {
		image = "myregistry.azurecr.io/test-image:latest"
	}
	return &azdext.ContainerPackageResponse{
		Result: &azdext.ServicePackageResult{
			Artifacts: []*azdext.Artifact{{
				Kind:         azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER,
				Location:     image,
				LocationKind: azdext.LocationKind_LOCATION_KIND_REMOTE,
			}},
		},
	}, nil
}

func (s *stubContainerServer) Publish(
	_ context.Context,
	request *azdext.ContainerPublishRequest,
) (*azdext.ContainerPublishResponse, error) {
	s.publishCalls.Add(1)
	s.pubRequest = request
	if s.publishErr != nil {
		return nil, s.publishErr
	}

	image := s.publishImage
	if image == "" {
		image = "myregistry.azurecr.io/test-image:latest"
	}
	return &azdext.ContainerPublishResponse{
		Result: &azdext.ServicePublishResult{
			Artifacts: []*azdext.Artifact{{
				Kind:         azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER,
				Location:     image,
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
	projectSrvs ...azdext.ProjectServiceServer,
) *azdext.AzdClient {
	t.Helper()

	srv := grpc.NewServer()
	if containerSrv != nil {
		azdext.RegisterContainerServiceServer(srv, containerSrv)
	}
	if promptSrv != nil {
		azdext.RegisterPromptServiceServer(srv, promptSrv)
	}
	if len(projectSrvs) > 0 && projectSrvs[0] != nil {
		azdext.RegisterProjectServiceServer(srv, projectSrvs[0])
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
	project     *azdext.ProjectConfig
	configValue *structpb.Value
}

func (s *stubProjectServer) GetServiceConfigValue(
	context.Context, *azdext.GetServiceConfigValueRequest,
) (*azdext.GetServiceConfigValueResponse, error) {
	return &azdext.GetServiceConfigValueResponse{Value: s.configValue, Found: s.configValue != nil}, nil
}

func TestDependencyConditionScalarValues(t *testing.T) {
	tests := []struct {
		name    string
		value   any
		enabled bool
	}{
		{name: "boolean false", value: false, enabled: false},
		{name: "boolean true", value: true, enabled: true},
		{name: "number zero", value: 0, enabled: false},
		{name: "number one", value: 1, enabled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, err := structpb.NewValue(tt.value)
			require.NoError(t, err)
			client := newServiceTargetTestClient(t, nil, nil, &stubProjectServer{configValue: value})
			provider := &AgentServiceTargetProvider{azdClient: client}

			enabled, err := provider.isDependencyEnabled(t.Context(), "tools")
			require.NoError(t, err)
			require.Equal(t, tt.enabled, enabled)
		})
	}
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

type legacyPreBuiltEnvironmentServer struct {
	azdext.UnimplementedEnvironmentServiceServer
}

func (s *legacyPreBuiltEnvironmentServer) GetValue(
	_ context.Context,
	_ *azdext.GetEnvRequest,
) (*azdext.KeyValueResponse, error) {
	return &azdext.KeyValueResponse{Value: "true"}, nil
}

func newLegacyPreBuiltTestClient(t *testing.T, promptSrv azdext.PromptServiceServer) *azdext.AzdClient {
	t.Helper()

	srv := grpc.NewServer()
	azdext.RegisterPromptServiceServer(srv, promptSrv)
	azdext.RegisterEnvironmentServiceServer(srv, &legacyPreBuiltEnvironmentServer{})

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

func TestInitializeValidatesRegistryConnectionLifecycle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		registry     bool
		docker       bool
		passthrough  bool
		remoteBuild  bool
		wantContains string
	}{
		{name: "registry with passthrough", registry: true, passthrough: true},
		{
			name:         "registry without docker",
			registry:     true,
			wantContains: "requires docker.imagePassthrough: true",
		},
		{
			name:         "registry with zero-value docker",
			registry:     true,
			docker:       true,
			wantContains: "requires docker.imagePassthrough: true",
		},
		{
			name:         "registry with remote build",
			registry:     true,
			passthrough:  true,
			remoteBuild:  true,
			wantContains: "cannot be combined with docker.remoteBuild",
		},
		{name: "legacy image without docker"},
		{name: "legacy image with remote build", docker: true, remoteBuild: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			agentDef := sampleContainerAgent()
			agentDef.Image = "registry.example.com/agents/my-agent:v1"
			agentDef.RegistryConnectionID = ""
			if test.registry {
				agentDef.RegistryConnectionID = "private-registry"
			}
			props, err := AgentDefinitionToServiceProperties(agentDef, nil)
			require.NoError(t, err)

			var dockerOptions *azdext.DockerProjectOptions
			if test.docker || test.passthrough || test.remoteBuild {
				dockerOptions = &azdext.DockerProjectOptions{
					RemoteBuild:      test.remoteBuild,
					ImagePassthrough: test.passthrough,
				}
			}
			provider := &AgentServiceTargetProvider{}
			err = provider.Initialize(t.Context(), &azdext.ServiceConfig{
				Name:                 "my-agent",
				Host:                 "azure.ai.agent",
				Image:                agentDef.Image,
				Docker:               dockerOptions,
				AdditionalProperties: props,
			})

			if test.wantContains != "" {
				require.ErrorContains(t, err, test.wantContains)
			} else {
				require.NoError(t, err)
			}
			require.Empty(t, provider.agentDefinitionPath)
			require.Nil(t, provider.credential)
			require.Empty(t, provider.tenantId)
		})
	}
}

func TestInitializeValidatesRegistryLifecycleFromRef(t *testing.T) {
	projectRoot := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "agent.yaml"),
		[]byte("kind: hosted\nname: ref-agent\nregistryConnectionId: private-registry\n"),
		0o600,
	))
	props, err := structpb.NewStruct(map[string]any{"$ref": "./agent.yaml"})
	require.NoError(t, err)
	provider := &AgentServiceTargetProvider{
		azdClient: newInitializeTestClient(t, projectRoot),
	}

	err = provider.Initialize(t.Context(), &azdext.ServiceConfig{
		Name:                 "ref-agent",
		Host:                 foundryAgentHost,
		Image:                "registry.example.com/team/agent:v1",
		AdditionalProperties: props,
	})
	require.ErrorContains(t, err, "requires docker.imagePassthrough: true")
}

func TestInitializeValidatesRegistryLifecycleFromLegacyDiskDefinition(t *testing.T) {
	projectRoot := t.TempDir()
	serviceDir := filepath.Join(projectRoot, "svc")
	require.NoError(t, os.MkdirAll(serviceDir, 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(serviceDir, "agent.yaml"),
		[]byte("kind: hosted\nname: disk-agent\nregistryConnectionId: private-registry\n"),
		0o600,
	))
	provider := &AgentServiceTargetProvider{
		azdClient: newInitializeTestClient(t, projectRoot),
	}

	err := provider.Initialize(t.Context(), &azdext.ServiceConfig{
		Name:         "disk-agent",
		Host:         foundryAgentHost,
		RelativePath: "svc",
		Image:        "registry.example.com/team/agent:v1",
	})
	require.ErrorContains(t, err, "requires docker.imagePassthrough: true")
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

func TestDeployTimeServiceConfigReplacesInitializeSnapshot(t *testing.T) {
	// azd core re-expands ${VAR} against the environment on every
	// call, so a deploy-time config can carry a value that was still
	// unset (and therefore expanded to "") when Initialize ran.
	// `azd up` initializes service targets before provisioning
	// prompts for a missing subscription or location, so keeping the
	// Initialize snapshot would deploy those empty strings.
	t.Setenv("AGENT_DEFINITION_PATH", "")

	projectRoot := t.TempDir()
	serviceDir := filepath.Join(projectRoot, "svc")
	require.NoError(t, os.MkdirAll(serviceDir, 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(serviceDir, "agent.yaml"),
		[]byte("kind: hostedAgent\n"),
		0o600,
	))

	provider := &AgentServiceTargetProvider{
		azdClient: newInitializeTestClient(t, projectRoot),
	}

	// AGENT_REGION references an unset variable at Initialize time.
	stale := &azdext.ServiceConfig{
		Name:         "echo",
		RelativePath: "svc",
		Environment:  map[string]string{"AGENT_REGION": ""},
	}
	require.NoError(t, provider.Initialize(t.Context(), stale))
	require.NoError(t, provider.ensureDeployContext(t.Context()))

	// The user is prompted during provision, so core hands the
	// deploy-time call a config with the persisted value.
	fresh := &azdext.ServiceConfig{
		Name:         "echo",
		RelativePath: "svc",
		Environment:  map[string]string{"AGENT_REGION": "westus2"},
	}

	// Deploy must adopt the config it was handed rather than reuse the
	// snapshot. It still fails further along (this stub has no Foundry
	// project), which is after the config has been adopted.
	_, err := provider.Deploy(
		t.Context(),
		fresh,
		&azdext.ServiceContext{},
		nil,
		func(string) {},
	)
	require.Error(t, err)
	require.Same(t, fresh, provider.serviceConfig)

	prep, err := provider.prepareDeploy(
		provider.serviceConfig,
		sampleContainerAgent(),
		map[string]string{"FOUNDRY_PROJECT_ENDPOINT": "https://project.example"},
		[]agent_yaml.AgentBuildOption{
			agent_yaml.WithImageURL("registry.example/agent:latest"),
		},
	)
	require.NoError(t, err)
	require.Equal(t, "westus2", prep.resolvedEnvVars["AGENT_REGION"])
}

func TestAdoptServiceConfigIgnoresNilAndKeepsResolvedState(t *testing.T) {
	t.Parallel()

	existing := &azdext.ServiceConfig{Name: "echo"}
	provider := &AgentServiceTargetProvider{
		serviceConfig:         existing,
		serviceConfigResolved: true,
	}

	// A nil config (or the same instance) must not drop the resolved
	// state, otherwise every repeat call would re-expand $ref
	// includes.
	provider.adoptServiceConfig(nil)
	require.Same(t, existing, provider.serviceConfig)
	require.True(t, provider.serviceConfigResolved)

	provider.adoptServiceConfig(existing)
	require.True(t, provider.serviceConfigResolved)

	provider.adoptServiceConfig(&azdext.ServiceConfig{Name: "echo"})
	require.False(t, provider.serviceConfigResolved)
}

func TestBuildVoiceWSProtocolURL(t *testing.T) {
	got := buildVoiceWSProtocolURL(
		"https://acct.services.ai.azure.com/api/projects/proj/",
		"voice-agent",
	)
	require.Equal(
		t,
		"wss://acct.services.ai.azure.com/api/projects/proj/agents/voice-agent/endpoint/protocols/voice?api-version=v1",
		got,
	)
}

func TestValidateVoiceAgentDeployResponse(t *testing.T) {
	t.Run("requires name and latest version", func(t *testing.T) {
		agent := &agent_api.AgentObject{Name: "voice-agent"}
		agent.Versions.Latest.Version = "1"
		err := validateVoiceAgentDeployResponse(agent)
		require.NoError(t, err)
	})

	t.Run("missing name rejected", func(t *testing.T) {
		err := validateVoiceAgentDeployResponse(&agent_api.AgentObject{})
		require.ErrorContains(t, err, "missing agent name")
	})

	t.Run("missing version rejected", func(t *testing.T) {
		err := validateVoiceAgentDeployResponse(&agent_api.AgentObject{Name: "voice-agent"})
		require.ErrorContains(t, err, "missing latest agent version")
	})
}

func TestShouldUpdateVoiceAgent(t *testing.T) {
	t.Run("remote found updates", func(t *testing.T) {
		update, err := shouldUpdateVoiceAgent(&agent_api.AgentObject{Name: "voice"}, nil)
		require.NoError(t, err)
		require.True(t, update)
	})

	t.Run("remote nil creates", func(t *testing.T) {
		update, err := shouldUpdateVoiceAgent(nil, nil)
		require.NoError(t, err)
		require.False(t, update)
	})

	t.Run("not found creates", func(t *testing.T) {
		update, err := shouldUpdateVoiceAgent(nil, &azcore.ResponseError{StatusCode: http.StatusNotFound})
		require.NoError(t, err)
		require.False(t, update)
	})

	t.Run("other get error returns error", func(t *testing.T) {
		update, err := shouldUpdateVoiceAgent(nil, &azcore.ResponseError{StatusCode: http.StatusInternalServerError})
		require.Error(t, err)
		require.False(t, update)
	})
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
	writes []*azdext.SetEnvRequest
}

func (s *stubEnvServer) SetValue(
	_ context.Context, req *azdext.SetEnvRequest,
) (*azdext.EmptyResponse, error) {
	if s.values == nil {
		s.values = make(map[string]string)
	}
	s.values[req.Key] = req.Value
	s.writes = append(s.writes, req)
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
		"",
		"",
		false,
		ActivityProfile{},
		nil,
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
	require.Equal(t, "https://proj.azure.com", envStub.values["AGENT_MY_SVC_PROJECT_ENDPOINT"])
	require.Equal(t, "AGENT_MY_SVC_VERSION", envStub.writes[0].Key)
	require.Empty(t, envStub.writes[0].Value)
	require.Equal(t, "AGENT_MY_SVC_VERSION", envStub.writes[len(envStub.writes)-1].Key)
	require.Equal(t, "1.0.0", envStub.writes[len(envStub.writes)-1].Value)
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
		"",
		"",
		false,
		ActivityProfile{},
		nil,
	)
	require.NoError(t, err)

	// Trailing slash must not produce a double-slash in the base endpoint
	require.Equal(t, "https://proj.azure.com/agents/my-agent/versions/2.0.0", envStub.values["AGENT_MY_SVC_ENDPOINT"])
}

func TestRegisterAgentEnvironmentVariables_PersistsActivityBotName(t *testing.T) {
	t.Parallel()

	envStub := &stubEnvServer{}
	provider := &AgentServiceTargetProvider{
		azdClient: newEnvTestClient(t, envStub),
		env:       &azdext.Environment{Name: "test-env"},
	}

	err := provider.registerAgentEnvironmentVariables(
		t.Context(),
		map[string]string{"FOUNDRY_PROJECT_ENDPOINT": "https://proj.azure.com"},
		&azdext.ServiceConfig{Name: "my-svc"},
		&agent_api.AgentVersionObject{Name: "my-agent", Version: "1.0.0"},
		nil,
		"published-bot",
		"bot-rg",
		true,
		ActivityProfile{},
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, "published-bot", envStub.values[envkey.AgentBotName("my-svc")])
	require.Equal(t, "bot-rg", envStub.values[envkey.AgentBotResourceGroup("my-svc")])
	require.Equal(t, "true", envStub.values[envkey.AgentBotOwned("my-svc")])
}

func TestRegisterAgentEnvironmentVariables_PersistsInstanceIdentity(t *testing.T) {
	t.Parallel()

	envStub := &stubEnvServer{}
	provider := &AgentServiceTargetProvider{
		azdClient: newEnvTestClient(t, envStub),
		env:       &azdext.Environment{Name: "test-env"},
	}

	err := provider.registerAgentEnvironmentVariables(
		t.Context(),
		map[string]string{"FOUNDRY_PROJECT_ENDPOINT": "https://proj.azure.com"},
		&azdext.ServiceConfig{Name: "my-svc"},
		&agent_api.AgentVersionObject{
			Name:    "my-agent",
			Version: "1.0.0",
			InstanceIdentity: &agent_api.AgentIdentityInfo{
				ClientID:    "client-id-123",
				PrincipalID: "principal-id-456",
			},
		},
		nil,
		"",
		"",
		false,
		ActivityProfile{},
		nil,
	)
	require.NoError(t, err)
	require.Equal(
		t,
		"client-id-123",
		envStub.values[envkey.AgentInstanceIdentityClientID("my-svc")],
	)
	require.Equal(
		t,
		"principal-id-456",
		envStub.values[envkey.AgentInstanceIdentityPrincipalID("my-svc")],
	)
}

type fakeActivityBotFinder struct {
	ref *botservice.BotReference
	err error
}

func (f fakeActivityBotFinder) FindByMsaAppID(
	_ context.Context,
	_ string,
) (*botservice.BotReference, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.ref, nil
}

func TestResolveActivityBotName_PrefersIdentityBoundBot(t *testing.T) {
	t.Parallel()

	p := &AgentServiceTargetProvider{}
	name, rg, err := p.resolveActivityBotName(
		t.Context(),
		fakeActivityBotFinder{ref: &botservice.BotReference{Name: "identity-bot", ResourceGroup: "identity-rg"}},
		"my-svc",
		"agent-a",
		"client-id-1",
		"fallback-rg",
		map[string]string{envkey.AgentBotName("my-svc"): "env-bot"},
	)

	require.NoError(t, err)
	require.Equal(t, "identity-bot", name)
	require.Equal(t, "identity-rg", rg)
}

func TestResolveActivityBotName_FallsBackToEnvValue(t *testing.T) {
	t.Parallel()

	p := &AgentServiceTargetProvider{}
	name, rg, err := p.resolveActivityBotName(
		t.Context(),
		fakeActivityBotFinder{},
		"my-svc",
		"agent-a",
		"client-id-1",
		"fallback-rg",
		map[string]string{envkey.AgentBotName("my-svc"): "env-bot"},
	)

	require.NoError(t, err)
	require.Equal(t, "env-bot", name)
	require.Equal(t, "", rg)
}

func TestResolveActivityBotName_FallsBackToDefaultName(t *testing.T) {
	t.Parallel()

	p := &AgentServiceTargetProvider{}
	name, rg, err := p.resolveActivityBotName(
		t.Context(),
		fakeActivityBotFinder{},
		"my-svc",
		"agent-a",
		"client-id-1",
		"fallback-rg",
		map[string]string{"AZURE_SUBSCRIPTION_ID": "sub-1"},
	)

	require.NoError(t, err)
	require.True(t, strings.HasPrefix(name, "agent-a-bot-"), name)
	require.Equal(t, "", rg)
}

func TestResolveActivityBotName_FallsBackWhenIdentityLookupFails(t *testing.T) {
	t.Parallel()

	p := &AgentServiceTargetProvider{}
	name, rg, err := p.resolveActivityBotName(
		t.Context(),
		fakeActivityBotFinder{err: errors.New("list not authorized")},
		"my-svc",
		"agent-a",
		"client-id-1",
		"fallback-rg",
		map[string]string{envkey.AgentBotName("my-svc"): "env-bot"},
	)

	require.NoError(t, err)
	require.Equal(t, "env-bot", name)
	require.Equal(t, "", rg)
}

func TestActivityBotOwnership(t *testing.T) {
	t.Parallel()

	t.Run("marks a newly created bot as azd-owned", func(t *testing.T) {
		t.Parallel()

		owned, tags := activityBotOwnership(false, nil)

		require.True(t, owned)
		require.Equal(t, botservice.OwnershipTagValue, *tags[botservice.OwnershipTag])
	})

	t.Run("preserves tags on an adopted bot", func(t *testing.T) {
		t.Parallel()

		existingTags := map[string]*string{"external": new("value")}
		owned, tags := activityBotOwnership(true, existingTags)

		require.False(t, owned)
		require.Equal(t, "value", *tags["external"])
		require.NotContains(t, tags, botservice.OwnershipTag)
	})

	t.Run("preserves azd ownership across redeploys", func(t *testing.T) {
		t.Parallel()

		existingTags := map[string]*string{botservice.OwnershipTag: new(botservice.OwnershipTagValue)}
		owned, tags := activityBotOwnership(true, existingTags)

		require.True(t, owned)
		require.Equal(t, botservice.OwnershipTagValue, *tags[botservice.OwnershipTag])
	})
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
		"",
		"",
		false,
		ActivityProfile{},
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
		"",
		"",
		false,
		ActivityProfile{},
		nil,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "agent version is empty")
}

func TestRegisterAgentEnvironmentVariables_PersistsDigitalWorkerBlueprintClientID(t *testing.T) {
	t.Parallel()

	envStub := &stubEnvServer{}
	provider := &AgentServiceTargetProvider{
		azdClient: newEnvTestClient(t, envStub),
		env:       &azdext.Environment{Name: "test-env"},
	}
	publish := &ActivityPublishConfig{PublishScope: "tenant"}

	err := provider.registerAgentEnvironmentVariables(
		t.Context(),
		map[string]string{"FOUNDRY_PROJECT_ENDPOINT": "https://proj.azure.com"},
		&azdext.ServiceConfig{Name: "my-svc"},
		&agent_api.AgentVersionObject{
			Name:    "my-agent",
			Version: "1.0.0",
			Blueprint: &agent_api.BlueprintInfo{
				ClientID: "blueprint-client-id",
			},
		},
		nil,
		"",
		"",
		false,
		ActivityProfile{IsActivity: true, UseCase: ActivityUseCaseDigitalWorker},
		&ActivitySettings{DigitalWorkerType: agent_api.DigitalWorkerTypeM365, Publish: publish},
	)
	require.NoError(t, err)
	require.Equal(t, "blueprint-client-id", envStub.values[envkey.AgentBlueprintClientID("my-svc")])
	require.Empty(t, envStub.values[envkey.AgentBotName("my-svc")])
	require.Empty(t, envStub.values[envkey.AgentBotResourceGroup("my-svc")])
	require.Empty(t, envStub.values[envkey.AgentBotOwned("my-svc")])
}

func TestDisplayableProtocolFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		protocol        string
		wantNil         bool
		wantProtocol    agent_api.AgentProtocol
		wantEnvSuffix   string
		wantURLContains string
		wantURLScheme   string // "https" or "wss"
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
			name:            "invocations_ws",
			protocol:        "invocations_ws",
			wantProtocol:    agent_api.AgentProtocolInvocationsWS,
			wantEnvSuffix:   "INVOCATIONS_WS",
			wantURLContains: "/api/projects/proj/agents/my-agent/endpoint/protocols/invocations_ws",
			wantURLScheme:   "wss",
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
		})
	}
}

func TestAgentInvocationEndpoints(t *testing.T) {
	t.Parallel()

	const endpoint = "https://myproject.services.ai.azure.com/api/projects/proj"
	const agentName = "my-agent"
	baseURL := endpoint + "/agents/" + agentName + "/endpoint/protocols/"

	const wsURL = "wss://myproject.services.ai.azure.com" +
		"/api/projects/proj/agents/" + agentName +
		"/endpoint/protocols/invocations_ws?api-version=" + agent_api.AgentEndpointAPIVersion

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
			name: "single invocations_ws protocol uses path-based form",
			protocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "invocations_ws", Version: "1.0.0"},
			},
			expected: []protocolEndpointInfo{
				{
					Protocol: "invocations_ws",
					URL:      wsURL,
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
					URL:      wsURL,
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
	require.Contains(t, got, "/api/projects/proj/agents/my-agent/endpoint/protocols/invocations_ws")
	require.Contains(t, got, "api-version="+agent_api.AgentEndpointAPIVersion)
}

func TestBuildInvocationsWSProtocolURL_TrimsTrailingSlash(t *testing.T) {
	t.Parallel()

	got := buildInvocationsWSProtocolURL(
		"https://myproject.services.ai.azure.com/api/projects/proj/",
		"my-agent",
	)
	require.Equal(t,
		"wss://myproject.services.ai.azure.com/api/projects/proj/agents/my-agent/"+
			"endpoint/protocols/invocations_ws?api-version="+agent_api.AgentEndpointAPIVersion,
		got,
	)
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
		ActivityProfile{},
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
		ActivityProfile{},
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
		ActivityProfile{},
		nil,
	)
	require.Empty(t, artifacts)
}

func TestDeployArtifacts_ActivityAgent_SkipsPlaygroundPortalLink(t *testing.T) {
	t.Parallel()

	p := &AgentServiceTargetProvider{}
	const ep = "https://myproject.services.ai.azure.com"

	protocols := []agent_yaml.ProtocolVersionRecord{
		{Protocol: "responses", Version: "1.0.0"},
	}

	artifacts := p.deployArtifacts(
		"activity-agent", "1.0.0",
		"/subscriptions/123/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj",
		ep,
		ActivityProfile{IsActivity: true, UseCase: ActivityUseCaseSimple},
		protocols,
	)

	require.Len(t, artifacts, 1)
	require.Equal(t, "Agent endpoint (responses)", artifacts[0].Metadata["label"])
	require.NotContains(t, artifacts[0].Location, "/build/agents/")
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

func TestPrepareDeployIncludesServiceEnvironment(t *testing.T) {
	t.Parallel()

	agentDef := sampleContainerAgent()
	agentDef.RegistryConnectionID = "private-registry"
	*agentDef.EnvironmentVariables = append(
		*agentDef.EnvironmentVariables,
		agent_yaml.EnvironmentVariable{
			Name:  "LEGACY_ONLY",
			Value: "${GLOBAL_VALUE}",
		},
		agent_yaml.EnvironmentVariable{
			Name:  "SHARED",
			Value: "${SHARED}",
		},
	)
	serviceConfig := &azdext.ServiceConfig{
		Name: "basic-agent",
		Environment: map[string]string{
			"SERVICE_ONLY": "literal ${NOT_A_TEMPLATE}",
			"SHARED":       "service",
		},
	}

	prep, err := (&AgentServiceTargetProvider{}).prepareDeploy(
		serviceConfig,
		agentDef,
		map[string]string{
			"FOUNDRY_PROJECT_ENDPOINT": "https://project.example",
			"GLOBAL_VALUE":             "legacy",
			"SHARED":                   "global",
		},
		[]agent_yaml.AgentBuildOption{
			agent_yaml.WithImageURL("registry.example/agent:latest"),
		},
	)
	require.NoError(t, err)
	require.Equal(
		t,
		"literal ${NOT_A_TEMPLATE}",
		prep.resolvedEnvVars["SERVICE_ONLY"],
	)
	require.Equal(t, "service", prep.resolvedEnvVars["SHARED"])
	require.Equal(t, "legacy", prep.resolvedEnvVars["LEGACY_ONLY"])
	hostedDefinition, ok := prep.request.Definition.(agent_api.HostedAgentDefinition)
	require.True(t, ok)
	require.NotNil(t, hostedDefinition.ContainerConfiguration)
	require.Equal(t, "private-registry", hostedDefinition.ContainerConfiguration.RegistryConnectionID)
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

func TestLoadContainerAgentDefinition_FileRef(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "agent.yaml"),
		[]byte(
			"kind: hosted\n"+
				"name: referenced-agent\n"+
				"protocols:\n"+
				"  - protocol: responses\n"+
				"    version: \"1.0.0\"\n",
		),
		0o600,
	))
	props, err := structpb.NewStruct(map[string]any{
		"$ref": "./agent.yaml",
	})
	require.NoError(t, err)
	provider := &AgentServiceTargetProvider{
		projectPath: dir,
		serviceConfig: &azdext.ServiceConfig{
			Name:                 "referenced-agent",
			Host:                 "azure.ai.agent",
			AdditionalProperties: props,
		},
	}

	got, isHosted, err := provider.loadContainerAgentDefinition()

	require.NoError(t, err)
	require.True(t, isHosted)
	require.Equal(t, "referenced-agent", got.Name)
}

func TestPackageBuildsContainerAgent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	agentPath := writeHostedAgentYAML(t, dir)
	containerStub := &stubContainerServer{}
	client := newContainerTestClient(t, containerStub)
	provider := &AgentServiceTargetProvider{
		azdClient:           client,
		agentDefinitionPath: agentPath,
		env:                 &azdext.Environment{Name: "test-env"},
		serviceConfig: &azdext.ServiceConfig{
			Name:         "referenced-agent",
			Host:         "azure.ai.agent",
			RelativePath: "src/agent",
		},
	}

	_, err := provider.Package(
		t.Context(),
		&azdext.ServiceConfig{Name: "referenced-agent"},
		&azdext.ServiceContext{},
		func(string) {},
	)

	require.NoError(t, err)
	require.Equal(t, int32(1), containerStub.buildCalls.Load())
	require.Equal(t, int32(1), containerStub.packageCalls.Load())
}

func TestPrepareDeployAppliesDefaultResources(t *testing.T) {
	t.Parallel()

	agentDef := sampleContainerAgent()
	agentDef.Resources = nil
	props, err := AgentDefinitionToServiceProperties(
		agentDef,
		&ServiceTargetAgentConfig{},
	)
	require.NoError(t, err)
	svc := &azdext.ServiceConfig{
		Name:                 "basic-agent",
		AdditionalProperties: props,
	}

	provider := &AgentServiceTargetProvider{}

	prep, err := provider.prepareDeploy(
		svc,
		agentDef,
		map[string]string{
			"FOUNDRY_PROJECT_ENDPOINT": "https://example",
		},
		[]agent_yaml.AgentBuildOption{
			agent_yaml.WithImageURL("registry.example/agent:v1"),
		},
	)

	require.NoError(t, err)
	definition, ok := prep.request.Definition.(agent_api.HostedAgentDefinition)
	require.True(t, ok)
	require.Equal(t, DefaultCpu, definition.CPU)
	require.Equal(t, DefaultMemory, definition.Memory)
}

func TestPrepareDeploySetsDigitalWorkerType(t *testing.T) {
	t.Parallel()

	agentDef := sampleContainerAgent()
	agentDef.Protocols = []agent_yaml.ProtocolVersionRecord{{Protocol: "activity", Version: "2.0.0"}}
	agentDef.AgentEndpoint = &agent_yaml.AgentEndpoint{
		Protocols: []string{"activity"},
		AuthorizationSchemes: []agent_yaml.AuthorizationScheme{
			{Type: string(agent_api.AgentEndpointAuthSchemeBotServiceRbac)},
		},
	}
	props, err := AgentDefinitionToServiceProperties(agentDef, &ServiceTargetAgentConfig{
		Activity: &ActivitySettings{DigitalWorkerType: agent_api.DigitalWorkerTypeM365},
	})
	require.NoError(t, err)
	svc := &azdext.ServiceConfig{
		Name:                 "digital-worker",
		AdditionalProperties: props,
	}

	prep, err := (&AgentServiceTargetProvider{}).prepareDeploy(
		svc,
		agentDef,
		map[string]string{"FOUNDRY_PROJECT_ENDPOINT": "https://example"},
		[]agent_yaml.AgentBuildOption{
			agent_yaml.WithImageURL("registry.example/worker:v1"),
		},
	)

	require.NoError(t, err)
	require.Equal(t, agent_api.DigitalWorkerTypeM365, prep.request.DigitalWorkerType)
	require.NotNil(t, prep.request.AgentEndpoint)
	require.Equal(
		t,
		[]agent_api.AgentEndpointProtocol{agent_api.AgentEndpointProtocolActivity},
		prep.request.AgentEndpoint.Protocols,
	)
	require.Len(t, prep.request.AgentEndpoint.AuthorizationSchemes, 1)
	require.Equal(t, agent_api.AgentEndpointAuthSchemeBotServiceRbac, prep.request.AgentEndpoint.AuthorizationSchemes[0].Type)
}

func TestPrepareDeployLeavesOmittedDigitalWorkerEndpointNil(t *testing.T) {
	t.Parallel()

	agentDef := sampleContainerAgent()
	agentDef.Protocols = []agent_yaml.ProtocolVersionRecord{{Protocol: "activity", Version: "2.0.0"}}
	agentDef.AgentEndpoint = nil
	agentDef.AgentCard = nil
	props, err := AgentDefinitionToServiceProperties(agentDef, &ServiceTargetAgentConfig{
		Activity: &ActivitySettings{DigitalWorkerType: agent_api.DigitalWorkerTypeM365},
	})
	require.NoError(t, err)
	svc := &azdext.ServiceConfig{
		Name:                 "digital-worker",
		AdditionalProperties: props,
	}

	prep, err := (&AgentServiceTargetProvider{}).prepareDeploy(
		svc,
		agentDef,
		map[string]string{"FOUNDRY_PROJECT_ENDPOINT": "https://example"},
		[]agent_yaml.AgentBuildOption{
			agent_yaml.WithImageURL("registry.example/worker:v1"),
		},
	)

	require.NoError(t, err)
	require.Equal(t, agent_api.DigitalWorkerTypeM365, prep.request.DigitalWorkerType)
	require.Nil(t, prep.request.AgentEndpoint)
	require.Nil(t, prep.request.AgentCard)
}

func TestPrepareDeployPreservesOmittedSimpleActivityAuthorizationSchemes(t *testing.T) {
	t.Parallel()

	agentDef := sampleContainerAgent()
	agentDef.Protocols = []agent_yaml.ProtocolVersionRecord{{Protocol: "activity", Version: "2.0.0"}}
	agentDef.AgentEndpoint = &agent_yaml.AgentEndpoint{Protocols: []string{"activity"}}
	props, err := AgentDefinitionToServiceProperties(agentDef, nil)
	require.NoError(t, err)
	svc := &azdext.ServiceConfig{
		Name:                 "simple-activity",
		AdditionalProperties: props,
	}

	prep, err := (&AgentServiceTargetProvider{}).prepareDeploy(
		svc,
		agentDef,
		map[string]string{"FOUNDRY_PROJECT_ENDPOINT": "https://example"},
		[]agent_yaml.AgentBuildOption{
			agent_yaml.WithImageURL("registry.example/activity:v1"),
		},
	)

	require.NoError(t, err)
	require.Empty(t, prep.request.DigitalWorkerType)
	require.NotNil(t, prep.request.AgentEndpoint)
	require.Equal(
		t,
		[]agent_api.AgentEndpointProtocol{agent_api.AgentEndpointProtocolActivity},
		prep.request.AgentEndpoint.Protocols,
	)
	require.Empty(t, prep.request.AgentEndpoint.AuthorizationSchemes)
}

func TestEnsureActivityEndpointAuthSchemeForPromotedDigitalWorkerPreservesExplicitScheme(t *testing.T) {
	t.Parallel()

	request := &agent_api.CreateAgentRequest{
		AgentEndpoint: &agent_api.AgentEndpoint{
			Protocols: []agent_api.AgentEndpointProtocol{agent_api.AgentEndpointProtocolActivity},
			AuthorizationSchemes: []agent_api.AgentEndpointAuthorizationScheme{
				{Type: agent_api.AgentEndpointAuthSchemeEntra},
				{Type: agent_api.AgentEndpointAuthSchemeBotServiceRbac},
			},
		},
	}

	ensureActivityEndpointAuthSchemeForProfile(request, ActivityProfile{
		IsActivity: true,
		UseCase:    ActivityUseCaseDigitalWorker,
	})

	require.Equal(t, []agent_api.AgentEndpointAuthorizationScheme{
		{Type: agent_api.AgentEndpointAuthSchemeEntra},
		{Type: agent_api.AgentEndpointAuthSchemeBotServiceRbac},
	}, request.AgentEndpoint.AuthorizationSchemes)
}

func TestEnsureActivityEndpointAuthSchemeForDigitalWorkerUsesServiceDefault(t *testing.T) {
	t.Parallel()

	request := &agent_api.CreateAgentRequest{}

	ensureActivityEndpointAuthSchemeForProfile(request, ActivityProfile{
		IsActivity: true,
		UseCase:    ActivityUseCaseDigitalWorker,
	})

	require.Nil(t, request.AgentEndpoint)
}

func TestEnsureActivityEndpointAuthSchemeForDigitalWorkerPreservesEndpointWithoutAddingScheme(t *testing.T) {
	t.Parallel()

	request := &agent_api.CreateAgentRequest{
		AgentEndpoint: &agent_api.AgentEndpoint{
			Protocols: []agent_api.AgentEndpointProtocol{agent_api.AgentEndpointProtocolResponses},
		},
	}

	ensureActivityEndpointAuthSchemeForProfile(request, ActivityProfile{
		IsActivity: true,
		UseCase:    ActivityUseCaseDigitalWorker,
	})

	require.Equal(t, []agent_api.AgentEndpointProtocol{
		agent_api.AgentEndpointProtocolResponses,
		agent_api.AgentEndpointProtocolActivity,
	}, request.AgentEndpoint.Protocols)
	require.Empty(t, request.AgentEndpoint.AuthorizationSchemes)
}

func TestEnsureActivityEndpointAuthSchemeForSimpleActivityUsesServiceDefault(t *testing.T) {
	t.Parallel()

	request := &agent_api.CreateAgentRequest{}

	ensureActivityEndpointAuthSchemeForProfile(request, ActivityProfile{
		IsActivity: true,
		UseCase:    ActivityUseCaseSimple,
	})

	require.Nil(t, request.AgentEndpoint)
}

func TestEnsureActivityEndpointAuthSchemePreservesExplicitLegacyBotService(t *testing.T) {
	t.Parallel()

	for _, useCase := range []ActivityUseCase{
		ActivityUseCaseDigitalWorker,
		ActivityUseCaseSimple,
	} {
		t.Run(string(useCase), func(t *testing.T) {
			request := &agent_api.CreateAgentRequest{
				AgentEndpoint: &agent_api.AgentEndpoint{
					AuthorizationSchemes: []agent_api.AgentEndpointAuthorizationScheme{
						{Type: agent_api.AgentEndpointAuthSchemeEntra},
						{Type: agent_api.AgentEndpointAuthSchemeBotService},
					},
				},
			}

			ensureActivityEndpointAuthSchemeForProfile(request, ActivityProfile{
				IsActivity: true,
				UseCase:    useCase,
			})

			require.Equal(t, []agent_api.AgentEndpointAuthorizationScheme{
				{Type: agent_api.AgentEndpointAuthSchemeEntra},
				{Type: agent_api.AgentEndpointAuthSchemeBotService},
			}, request.AgentEndpoint.AuthorizationSchemes)
		})
	}
}

func TestEnsureActivityEndpointAuthSchemeForNonActivityDoesNotCreateEndpoint(t *testing.T) {
	t.Parallel()

	request := &agent_api.CreateAgentRequest{}

	ensureActivityEndpointAuthSchemeForProfile(request, ActivityProfile{})

	require.Nil(t, request.AgentEndpoint)
}

func TestActivityProfileFromCreateRequest(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		request *agent_api.CreateAgentRequest
		want    ActivityProfile
	}{
		{
			name: "digital worker",
			request: &agent_api.CreateAgentRequest{
				CreateAgentVersionRequest: agent_api.CreateAgentVersionRequest{
					DigitalWorkerType: agent_api.DigitalWorkerTypeM365,
				},
			},
			want: ActivityProfile{IsActivity: true, UseCase: ActivityUseCaseDigitalWorker},
		},
		{
			name: "simple activity",
			request: &agent_api.CreateAgentRequest{
				AgentEndpoint: &agent_api.AgentEndpoint{
					Protocols: []agent_api.AgentEndpointProtocol{agent_api.AgentEndpointProtocolActivity},
				},
			},
			want: ActivityProfile{IsActivity: true, UseCase: ActivityUseCaseSimple},
		},
		{
			name: "non activity",
			request: &agent_api.CreateAgentRequest{
				AgentEndpoint: &agent_api.AgentEndpoint{
					Protocols: []agent_api.AgentEndpointProtocol{agent_api.AgentEndpointProtocolResponses},
				},
			},
			want: ActivityProfile{},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, activityProfileFromCreateRequest(test.request))
		})
	}
}

func TestValidateRegistryConnectionDefinition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		agent       agent_yaml.ContainerAgent
		wantContain string
	}{
		{name: "unset"},
		{
			name: "valid",
			agent: agent_yaml.ContainerAgent{
				Image: "registry.example.com/agent:v1", RegistryConnectionID: "private-registry",
			},
		},
		{
			name: "missing image", agent: agent_yaml.ContainerAgent{RegistryConnectionID: "private-registry"},
			wantContain: "requires a pre-built container image",
		},
		{
			name: "unqualified image",
			agent: agent_yaml.ContainerAgent{
				Image: "agent:v1", RegistryConnectionID: "private-registry",
			},
			wantContain: "explicit registry host and repository",
		},
		{
			name: "image URL scheme",
			agent: agent_yaml.ContainerAgent{
				Image: "https://registry.example.com/agent:v1", RegistryConnectionID: "private-registry",
			},
			wantContain: "explicit registry host and repository",
		},
		{
			name: "code deploy",
			agent: agent_yaml.ContainerAgent{
				Image: "registry.example.com/agent:v1", RegistryConnectionID: "private-registry",
				CodeConfiguration: &agent_yaml.CodeConfiguration{},
			},
			wantContain: "codeConfiguration",
		},
		{
			name: "whitespace", agent: agent_yaml.ContainerAgent{RegistryConnectionID: "  "},
			wantContain: "empty or whitespace",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := validateRegistryConnectionDefinition(test.agent)
			if test.wantContain == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, test.wantContain)
		})
	}
}

func TestShouldUsePreBuiltImage_NoImageDefaultsToBuild(t *testing.T) {
	t.Parallel()

	provider := &AgentServiceTargetProvider{}

	result, err := provider.shouldUsePreBuiltImage(t.Context(), agent_yaml.ContainerAgent{})
	require.NoError(t, err)
	require.False(t, result, "should default to build when no image is configured")
}

func TestShouldUsePreBuiltImage_LegacyInitImageUsesCompatibilityMarker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		docker *azdext.DockerProjectOptions
	}{
		{name: "docker absent"},
		{name: "mapped zero-value docker", docker: &azdext.DockerProjectOptions{}},
		{name: "configured docker", docker: &azdext.DockerProjectOptions{RemoteBuild: true}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			promptStub := &stubPromptServer{selectedIndex: 0}
			provider := &AgentServiceTargetProvider{
				azdClient: newLegacyPreBuiltTestClient(t, promptStub),
				env:       &azdext.Environment{Name: "test-env"},
				serviceConfig: &azdext.ServiceConfig{
					Docker: test.docker,
				},
			}

			result, err := provider.shouldUsePreBuiltImage(t.Context(), agent_yaml.ContainerAgent{
				Image: "registry.example.com/agent:v1",
			})
			require.NoError(t, err)
			require.True(t, result)
			require.Equal(t, int32(0), promptStub.selectCalls.Load())
		})
	}
}

func TestShouldUsePreBuiltImage_RegistryConnectionForcesPreBuilt(t *testing.T) {
	t.Parallel()

	promptStub := &stubPromptServer{selectedIndex: 0}
	provider := &AgentServiceTargetProvider{azdClient: newPromptTestClient(t, promptStub)}
	result, err := provider.shouldUsePreBuiltImage(t.Context(), agent_yaml.ContainerAgent{
		Image:                "registry.example.com/agent:v1",
		RegistryConnectionID: "private-registry",
	})
	require.NoError(t, err)
	require.True(t, result)
	require.Equal(t, int32(0), promptStub.selectCalls.Load(), "registry-backed images must not prompt to build")
}

func TestShouldUsePreBuiltImage_RegistryConnectionRequiresImage(t *testing.T) {
	t.Parallel()

	provider := &AgentServiceTargetProvider{}
	_, err := provider.shouldUsePreBuiltImage(t.Context(), agent_yaml.ContainerAgent{
		RegistryConnectionID: "private-registry",
	})
	require.ErrorContains(t, err, "requires a pre-built container image")
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

func TestPackage_DelegatesImagePassthroughToCore(t *testing.T) {
	const image = "registry.example.com/agents/my-agent:v1"
	tests := []struct {
		name               string
		registryConnection string
	}{
		{name: "BYO image"},
		{name: "private registry image", registryConnection: "production-registry"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			agentPath := filepath.Join(dir, "agent.yaml")
			content := fmt.Sprintf("kind: hosted\nname: test-agent\nimage: %s\n", image)
			if test.registryConnection != "" {
				content += fmt.Sprintf("registryConnectionId: %s\n", test.registryConnection)
			}
			require.NoError(t, os.WriteFile(agentPath, []byte(content), 0o600))

			containerStub := &stubContainerServer{packageImage: image}
			promptStub := &stubPromptServer{selectedIndex: 0}
			dockerOptions := &azdext.DockerProjectOptions{ImagePassthrough: true}
			provider := &AgentServiceTargetProvider{
				azdClient:           newServiceTargetTestClient(t, containerStub, promptStub),
				agentDefinitionPath: agentPath,
				env:                 &azdext.Environment{Name: "test-env"},
			}

			result, err := provider.Package(
				t.Context(),
				&azdext.ServiceConfig{Name: "test-svc", Docker: dockerOptions},
				&azdext.ServiceContext{},
				func(string) {},
			)

			require.NoError(t, err)
			require.Len(t, result.Artifacts, 1)
			require.Equal(t, image, result.Artifacts[0].Location)
			require.Equal(t, azdext.LocationKind_LOCATION_KIND_REMOTE, result.Artifacts[0].LocationKind)
			require.Equal(t, int32(0), containerStub.buildCalls.Load())
			require.Equal(t, int32(1), containerStub.packageCalls.Load())
			require.Equal(t, int32(0), promptStub.selectCalls.Load())
		})
	}
}

func TestPackage_ReusesCoreImagePassthroughArtifact(t *testing.T) {
	t.Parallel()

	const image = "registry.example.com/agents/my-agent:v1"
	dir := t.TempDir()
	agentPath := writeHostedAgentYAMLWithImage(t, dir, image)
	containerStub := &stubContainerServer{packageImage: image}
	dockerOptions := &azdext.DockerProjectOptions{ImagePassthrough: true}
	provider := &AgentServiceTargetProvider{
		azdClient:           newContainerTestClient(t, containerStub),
		agentDefinitionPath: agentPath,
		env:                 &azdext.Environment{Name: "test-env"},
	}
	serviceContext := &azdext.ServiceContext{Package: []*azdext.Artifact{{
		Kind:         azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER,
		Location:     image,
		LocationKind: azdext.LocationKind_LOCATION_KIND_REMOTE,
		Metadata:     map[string]string{"imagePassthrough": "true"},
	}}}

	result, err := provider.Package(
		t.Context(),
		&azdext.ServiceConfig{Name: "test-svc", Docker: dockerOptions},
		serviceContext,
		func(string) {},
	)

	require.NoError(t, err)
	require.Empty(t, result.Artifacts, "core already added the passthrough artifact to the shared context")
	require.Len(t, serviceContext.Package, 1)
	require.Equal(t, int32(0), containerStub.packageCalls.Load())
}

func TestPackage_CodeDeployTakesPrecedenceOverImagePassthrough(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	agentPath := filepath.Join(dir, "agent.yaml")
	require.NoError(t, os.WriteFile(agentPath, []byte(`kind: hosted
name: test-agent
image: registry.example.com/agents/test-agent:v1
code_configuration:
  runtime: python_3_13
  entry_point: app.py
`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.py"), []byte("print('hello')\n"), 0o600))

	containerStub := &stubContainerServer{}
	dockerOptions := &azdext.DockerProjectOptions{ImagePassthrough: true}
	provider := &AgentServiceTargetProvider{
		azdClient:           newContainerTestClient(t, containerStub),
		agentDefinitionPath: agentPath,
		env:                 &azdext.Environment{Name: "test-env"},
	}

	result, err := provider.Package(
		t.Context(),
		&azdext.ServiceConfig{Name: "test-svc", Docker: dockerOptions},
		&azdext.ServiceContext{},
		func(string) {},
	)

	require.NoError(t, err)
	require.Len(t, result.Artifacts, 1)
	require.Equal(t, azdext.ArtifactKind_ARTIFACT_KIND_ARCHIVE, result.Artifacts[0].Kind)
	require.Equal(t, "code-zip", result.Artifacts[0].Metadata["type"])
	require.Equal(t, int32(0), containerStub.buildCalls.Load())
	require.Equal(t, int32(0), containerStub.packageCalls.Load())
	t.Cleanup(func() { require.NoError(t, os.Remove(result.Artifacts[0].Location)) })
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

func TestPublish_DelegatesImagePassthroughToCore(t *testing.T) {
	t.Parallel()

	const image = "registry.example.com/agents/my-agent:v1"
	dir := t.TempDir()
	agentPath := writeHostedAgentYAMLWithImage(t, dir, image)
	containerStub := &stubContainerServer{publishImage: image}
	dockerOptions := &azdext.DockerProjectOptions{ImagePassthrough: true}
	provider := &AgentServiceTargetProvider{
		azdClient:           newContainerTestClient(t, containerStub),
		agentDefinitionPath: agentPath,
		env:                 &azdext.Environment{Name: "test-env"},
	}

	result, err := provider.Publish(
		t.Context(),
		&azdext.ServiceConfig{Name: "test-svc", Docker: dockerOptions},
		&azdext.ServiceContext{Package: []*azdext.Artifact{{
			Kind:         azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER,
			Location:     image,
			LocationKind: azdext.LocationKind_LOCATION_KIND_REMOTE,
			Metadata:     map[string]string{"imagePassthrough": "true"},
		}}},
		&azdext.TargetResource{},
		&azdext.PublishOptions{},
		func(string) {},
	)

	require.NoError(t, err)
	require.Len(t, result.Artifacts, 1)
	require.Equal(t, image, result.Artifacts[0].Location)
	require.Equal(t, int32(1), containerStub.publishCalls.Load())
}

func TestPublish_CodeDeployTakesPrecedenceOverPreBuiltArtifact(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	agentPath := filepath.Join(dir, "agent.yaml")
	require.NoError(t, os.WriteFile(agentPath, []byte(`kind: hosted
name: test-agent
image: registry.example.com/agents/test-agent:v1
code_configuration:
  runtime: python_3_13
  entry_point: app.py
`), 0o600))

	containerStub := &stubContainerServer{}
	provider := &AgentServiceTargetProvider{
		azdClient:           newContainerTestClient(t, containerStub),
		agentDefinitionPath: agentPath,
		env:                 &azdext.Environment{Name: "test-env"},
	}

	result, err := provider.Publish(
		t.Context(),
		&azdext.ServiceConfig{Name: "test-svc"},
		&azdext.ServiceContext{Package: []*azdext.Artifact{
			preBuiltImageArtifact("registry.example.com/agents/test-agent:v1"),
		}},
		&azdext.TargetResource{},
		&azdext.PublishOptions{},
		func(string) {},
	)

	require.NoError(t, err)
	require.Empty(t, result.Artifacts)
	require.Equal(t, int32(0), containerStub.publishCalls.Load())
}

func TestPublish_SkipsWhenPreBuiltImageChosen(t *testing.T) {
	t.Parallel()

	imageURL := "myregistry.azurecr.io/myimage:v1"
	dir := t.TempDir()
	agentPath := writeHostedAgentYAMLWithImage(t, dir, imageURL)

	provider := &AgentServiceTargetProvider{
		azdClient:           newContainerTestClient(t, &stubContainerServer{}),
		agentDefinitionPath: agentPath,
		env:                 &azdext.Environment{Name: "test-env"},
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
		serviceConfig: &azdext.ServiceConfig{
			Name:         "test-svc",
			RelativePath: "src/agent",
		},
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

func TestPublish_PreservesHostServiceDetails(t *testing.T) {
	t.Parallel()

	st := status.New(codes.Unknown, "registry request failed")
	withDetails, err := st.WithDetails(&azdext.ServiceErrorDetail{
		ErrorCode:   "TooManyRequests",
		StatusCode:  429,
		ServiceName: "management.azure.com",
	})
	require.NoError(t, err)

	publishErr := publishWithContainerError(t, withDetails.Err())

	serviceErr, ok := errors.AsType[*azdext.ServiceError](publishErr)
	require.True(t, ok)
	require.Equal(t, "container_publish.TooManyRequests", serviceErr.ErrorCode)
	require.Equal(t, 429, serviceErr.StatusCode)
	require.Equal(t, "management.azure.com", serviceErr.ServiceName)
}

func TestPublish_PreservesRelayedHostServiceError(t *testing.T) {
	t.Parallel()

	source := &azdext.ServiceError{
		Message:     "could not get Foundry project",
		ErrorCode:   "get_foundry_project.AuthorizationFailed",
		StatusCode:  403,
		ServiceName: "management.azure.com",
		Suggestion:  "request the required role",
	}
	st, err := status.New(codes.Unknown, "container publish failed").WithDetails(azdext.WrapError(source))
	require.NoError(t, err)

	publishErr := publishWithContainerError(t, st.Err())

	serviceErr, ok := errors.AsType[*azdext.ServiceError](publishErr)
	require.True(t, ok)
	require.Equal(t, source.Message, serviceErr.Message)
	require.Equal(t, source.ErrorCode, serviceErr.ErrorCode)
	require.Equal(t, source.StatusCode, serviceErr.StatusCode)
	require.Equal(t, source.ServiceName, serviceErr.ServiceName)
	require.Equal(t, source.Suggestion, serviceErr.Suggestion)
}

func TestPublish_PreservesRelayedHostLocalError(t *testing.T) {
	t.Parallel()

	source := &azdext.LocalError{
		Message:    "invalid Foundry project resource ID",
		Code:       "invalid_ai_project_id",
		Category:   azdext.LocalErrorCategoryValidation,
		Suggestion: "verify AZURE_AI_PROJECT_ID",
	}
	st, err := status.New(codes.Unknown, "container publish failed").WithDetails(azdext.WrapError(source))
	require.NoError(t, err)

	publishErr := publishWithContainerError(t, st.Err())

	localErr, ok := errors.AsType[*azdext.LocalError](publishErr)
	require.True(t, ok)
	require.Equal(t, source.Message, localErr.Message)
	require.Equal(t, source.Code, localErr.Code)
	require.Equal(t, source.Category, localErr.Category)
	require.Equal(t, source.Suggestion, localErr.Suggestion)
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

// endpointsTestEnvServer serves GetCurrent/GetValues for Endpoints() tests.
type endpointsTestEnvServer struct {
	azdext.UnimplementedEnvironmentServiceServer
	values map[string]string
}

func (s *endpointsTestEnvServer) GetCurrent(
	context.Context, *azdext.EmptyRequest,
) (*azdext.EnvironmentResponse, error) {
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: "test-env"}}, nil
}

func (s *endpointsTestEnvServer) GetValues(
	context.Context, *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	kvs := make([]*azdext.KeyValue, 0, len(s.values))
	for k, v := range s.values {
		kvs = append(kvs, &azdext.KeyValue{Key: k, Value: v})
	}
	return &azdext.KeyValueListResponse{KeyValues: kvs}, nil
}

func newEndpointsTestClient(
	t *testing.T, projectRoot string, envValues map[string]string,
) *azdext.AzdClient {
	t.Helper()

	srv := grpc.NewServer()
	azdext.RegisterProjectServiceServer(srv, &stubProjectServer{
		project: &azdext.ProjectConfig{Path: projectRoot},
	})
	azdext.RegisterEnvironmentServiceServer(srv, &endpointsTestEnvServer{values: envValues})

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

// TestEndpoints_VoiceManifestOnDisk_ResolvesProjectRoot covers the fresh-process
// case where Endpoints runs without ensureDeployContext having populated
// p.projectPath. A legacy-shape prompt-voice service (kind only on disk, no
// inline kind) may retain NAME+ENDPOINT without VERSION from an earlier deploy;
// Endpoints must resolve the project root itself so agentkind classifies it as
// voice and returns the base endpoint instead of the missing-VERSION error.
func TestEndpoints_VoiceManifestOnDisk_ResolvesProjectRoot(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	serviceDir := filepath.Join(projectRoot, "src", "voice")
	require.NoError(t, os.MkdirAll(serviceDir, 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(serviceDir, "agent.yaml"),
		[]byte("kind: prompt-voice\nname: my-voice\n"),
		0o600,
	))

	const endpoint = "https://proj.services.ai.azure.com/voice/my-voice"
	client := newEndpointsTestClient(t, projectRoot, map[string]string{
		"FOUNDRY_PROJECT_ENDPOINT": "https://proj.services.ai.azure.com",
		"AGENT_VOICE_NAME":         "my-voice",
		"AGENT_VOICE_ENDPOINT":     endpoint,
		// Deliberately model a legacy persisted environment with no VERSION.
	})

	// Fresh process: projectPath/agentDefinitionPath are empty, exactly as they
	// are before any ensureDeployContext call.
	provider := &AgentServiceTargetProvider{azdClient: client}

	got, err := provider.Endpoints(
		t.Context(),
		&azdext.ServiceConfig{Name: "voice", RelativePath: "src/voice"},
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, []string{endpoint}, got)
}

// TestEndpoints_VoiceAgentDefinitionPathOverride covers the fresh-process case
// where a voice manifest is supplied via the AGENT_DEFINITION_PATH override.
// Endpoints runs without ensureDeployContext (so p.agentDefinitionPath is empty)
// and must read the process override to classify a legacy persisted
// NAME+ENDPOINT environment as voice, rather than classifying the (kind-less)
// service entry and returning missing-VERSION.
func TestEndpoints_VoiceAgentDefinitionPathOverride(t *testing.T) {
	projectRoot := t.TempDir()
	overridePath := filepath.Join(projectRoot, "custom-voice.yaml")
	require.NoError(t, os.WriteFile(
		overridePath,
		[]byte("kind: prompt-voice\nname: my-voice\n"),
		0o600,
	))
	t.Setenv("AGENT_DEFINITION_PATH", overridePath)

	const endpoint = "https://proj.services.ai.azure.com/voice/my-voice"
	client := newEndpointsTestClient(t, projectRoot, map[string]string{
		"FOUNDRY_PROJECT_ENDPOINT": "https://proj.services.ai.azure.com",
		"AGENT_VOICE_NAME":         "my-voice",
		"AGENT_VOICE_ENDPOINT":     endpoint,
		// Deliberately model a legacy persisted environment with no VERSION.
	})

	// Fresh process: the service entry carries no kind; only the override does.
	provider := &AgentServiceTargetProvider{azdClient: client}

	got, err := provider.Endpoints(
		t.Context(),
		&azdext.ServiceConfig{Name: "voice", RelativePath: "src/voice"},
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, []string{endpoint}, got)
}

// resolution added for voice does not change hosted behavior: a hosted service
// (no voice manifest) with a lingering ENDPOINT but no VERSION must still
// surface the actionable missing-env-vars error.
func TestEndpoints_HostedMissingVersion_StillErrors(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "src", "hosted"), 0o750))

	client := newEndpointsTestClient(t, projectRoot, map[string]string{
		"FOUNDRY_PROJECT_ENDPOINT": "https://proj.services.ai.azure.com",
		"AGENT_HOSTED_ENDPOINT":    "https://proj.services.ai.azure.com/agents/hosted",
		// no VERSION and no voice manifest -> must error, not fall through.
	})

	provider := &AgentServiceTargetProvider{azdClient: client}

	_, err := provider.Endpoints(
		t.Context(),
		&azdext.ServiceConfig{Name: "hosted", RelativePath: "src/hosted"},
		nil,
	)
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Equal(t, exterrors.CodeMissingAgentEnvVars, localErr.Code)
}
