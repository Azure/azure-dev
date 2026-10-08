// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

type previewEnvironmentServer struct {
	azdext.UnimplementedEnvironmentServiceServer
	values map[string]string
}

func (s *previewEnvironmentServer) GetCurrent(
	context.Context, *azdext.EmptyRequest,
) (*azdext.EnvironmentResponse, error) {
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: "selected"}}, nil
}

func (s *previewEnvironmentServer) GetValues(
	_ context.Context, request *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	if request.Name != "selected" {
		return nil, status.Error(codes.InvalidArgument, "wrong environment")
	}
	values := []*azdext.KeyValue{}
	for key, value := range s.values {
		values = append(values, &azdext.KeyValue{Key: key, Value: value})
	}
	return &azdext.KeyValueListResponse{KeyValues: values}, nil
}

type previewTenantServer struct {
	azdext.UnimplementedAccountServiceServer
	err error
}

func (s *previewTenantServer) LookupTenant(
	_ context.Context, request *azdext.LookupTenantRequest,
) (*azdext.LookupTenantResponse, error) {
	if request.SubscriptionId != "subscription" {
		return nil, status.Error(codes.InvalidArgument, "wrong subscription")
	}
	if s.err != nil {
		return nil, s.err
	}
	return &azdext.LookupTenantResponse{TenantId: "user-access-tenant-not-resource-tenant"}, nil
}

func TestProviderPreviewWithoutInitializeIsReadOnly(t *testing.T) {
	for _, tc := range []struct {
		name        string
		legacy      bool
		authError   bool
		remoteCode  int
		wantError   bool
		resolvedRef bool
	}{
		{name: "modern create", remoteCode: http.StatusNotFound},
		{name: "modern existing"},
		{name: "legacy rejected before auth", legacy: true, wantError: true},
		{name: "no prompt authentication failure", authError: true, wantError: true},
		{name: "authorization failure", remoteCode: http.StatusForbidden, wantError: true},
		{name: "resolved transport retains original provenance", resolvedRef: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AGENT_DEFINITION_PATH", "")
			t.Setenv("AZD_NO_PROMPT", "true")
			root := t.TempDir()
			azureYAML := []byte("name: preview\nservices:\n  agent:\n    host: azure.ai.agent\n")
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), azureYAML, 0600))
			require.NoError(t, os.WriteFile(filepath.Join(root, "agent.yaml"), []byte("retained: unused\n"), 0600))
			service := previewService(t)
			if tc.legacy {
				service.Config, service.AdditionalProperties = service.AdditionalProperties, nil
			}
			before := proto.CloneOf(service)
			original := proto.CloneOf(service)
			if tc.resolvedRef {
				require.NoError(t, os.WriteFile(filepath.Join(root, "agent.yaml"),
					[]byte("kind: hosted\nname: example-agent\n"), 0600))
				original.AdditionalProperties.Fields["$ref"] = structpb.NewStringValue("agent.yaml")
			}
			allowed := map[string]bool{
				"/azd.extensions.v1.ProjectService/Get":            true,
				"/azd.extensions.v1.EnvironmentService/GetCurrent": true,
				"/azd.extensions.v1.EnvironmentService/GetValues":  true,
				"/azd.extensions.v1.AccountService/LookupTenant":   true,
			}
			var mu sync.Mutex
			var calls []string
			server := grpc.NewServer(grpc.UnaryInterceptor(func(
				ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
			) (any, error) {
				mu.Lock()
				calls = append(calls, info.FullMethod)
				mu.Unlock()
				if !allowed[info.FullMethod] {
					return nil, status.Error(codes.PermissionDenied, "preview attempted an unexpected host operation")
				}
				return handler(ctx, req)
			}))
			azdext.RegisterProjectServiceServer(server, &stubProjectServer{
				project: &azdext.ProjectConfig{
					Name: "preview", Path: root, Services: map[string]*azdext.ServiceConfig{"agent": original},
				},
			})
			azdext.RegisterEnvironmentServiceServer(server, &previewEnvironmentServer{values: map[string]string{
				"AZURE_SUBSCRIPTION_ID": "subscription",
				"FOUNDRY_PROJECT_ENDPOINT": "https://private-user:private-password@account.services.ai.azure.com/" +
					"api/projects/project?sig=private-sig#private-fragment",
			}})
			tenantServer := &previewTenantServer{}
			if tc.authError {
				tenantServer.err = status.Error(codes.Unauthenticated,
					"sensitive auth failure https://private-user:private-password@host?sig=private-sig#private-fragment")
			}
			azdext.RegisterAccountServiceServer(server, tenantServer)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(func() { server.Stop(); _ = listener.Close() })
			client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
			require.NoError(t, err)
			t.Cleanup(func() { client.Close() })
			provider := NewAgentServiceTargetProvider(client).(*AgentServiceTargetProvider)
			factoryCalls := 0
			reader := &recordingPreviewReader{agent: remotePreviewAgent(previewRequest(t))}
			if tc.remoteCode != 0 {
				reader.agent = nil
				reader.err = &azcore.ResponseError{StatusCode: tc.remoteCode}
			}
			provider.previewReader = func(endpoint, tenant string) (agentPreviewReader, error) {
				factoryCalls++
				require.Equal(t, "user-access-tenant-not-resource-tenant", tenant)
				require.Equal(t, "https://account.services.ai.azure.com/api/projects/project", endpoint)
				return reader, nil
			}
			wire, err := proto.Marshal(service)
			require.NoError(t, err)
			betaService := &v1beta.ServiceConfig{}
			require.NoError(t, proto.Unmarshal(wire, betaService))
			result, err := provider.Preview(t.Context(), betaService)
			if tc.wantError {
				require.Error(t, err)
				require.Nil(t, result)
				require.NotContains(t, err.Error(), "private-")
				require.NotContains(t, err.Error(), "sensitive")
			} else {
				require.NoError(t, err)
				require.NotNil(t, result.Data)
				require.NotContains(t, result.Message, "private-")
			}
			if tc.legacy || tc.authError || tc.resolvedRef {
				require.Zero(t, factoryCalls)
				require.Zero(t, reader.calls)
			} else {
				require.Equal(t, 1, factoryCalls)
				require.Equal(t, 1, reader.calls)
			}
			mu.Lock()
			recorded := append([]string(nil), calls...)
			mu.Unlock()
			for _, call := range recorded {
				require.True(t, allowed[call], "unexpected operation: %s", call)
			}
			if tc.legacy || tc.resolvedRef {
				require.Len(t, recorded, 1, "legacy source must be rejected before environment/auth reads")
			}
			require.True(t, proto.Equal(before, service))
			after, err := proto.Marshal(betaService)
			require.NoError(t, err)
			stableAfter := &azdext.ServiceConfig{}
			require.NoError(t, proto.Unmarshal(after, stableAfter))
			require.True(t, proto.Equal(before, stableAfter), "preview must not mutate the beta request")
			require.Nil(t, provider.serviceConfig)
			require.Nil(t, provider.env)
			require.Nil(t, provider.credential)
			require.False(t, provider.deployContextReady)
			require.Empty(t, provider.agentDefinitionPath)
			entries, err := os.ReadDir(root)
			require.NoError(t, err)
			require.Len(t, entries, 2, "preview must not create .azure, lock, artifact, or state files")
			persisted, err := os.ReadFile(filepath.Join(root, "azure.yaml"))
			require.NoError(t, err)
			require.Equal(t, azureYAML, persisted)
		})
	}
}

func TestPreviewRemoteMalformedJSONDefinition(t *testing.T) {
	request := previewRequest(t)
	remote := remotePreviewAgent(request)
	remote.Versions.Latest.Definition = map[string]any{
		"kind": "hosted", "cpu": "0.5", "memory": "1Gi",
		"container_configuration": map[string]any{"image": map[string]any{"secret": "sensitive"}},
	}
	_, err := previewAgentRequest(t.Context(), &recordingPreviewReader{agent: remote}, "agent", request, nil)
	require.ErrorContains(t, err, "invalid hosted-agent definition")
	require.NotContains(t, err.Error(), "sensitive")
}

func TestPreviewRemoteEnvironmentRemoval(t *testing.T) {
	request := previewRequest(t)
	remote := remotePreviewAgent(request)
	hosted := remote.Versions.Latest.Definition.(agent_api.HostedAgentDefinition)
	hosted.EnvironmentVariables = map[string]string{"OLD_SECRET": "remote-secret"}
	remote.Versions.Latest.Definition = hosted
	result, err := comparePreviewRequest("agent", request, remote, nil)
	require.NoError(t, err)
	require.Contains(t, result.Message, "remove: definition.environment_variables.OLD_SECRET")
	require.NotContains(t, result.Message, "remote-secret")
}
