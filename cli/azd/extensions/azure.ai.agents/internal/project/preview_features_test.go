// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/featureflags"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

type previewConfigServer struct {
	azdext.UnimplementedUserConfigServiceServer
	alpha map[string]any
}

func (s *previewConfigServer) Get(
	context.Context, *azdext.GetUserConfigRequest,
) (*azdext.GetUserConfigResponse, error) {
	value, err := json.Marshal(s.alpha)
	return &azdext.GetUserConfigResponse{Found: s.alpha != nil, Value: value}, err
}

type previewProjectServer struct {
	azdext.UnimplementedProjectServiceServer
}

func (*previewProjectServer) Get(context.Context, *azdext.EmptyRequest) (*azdext.GetProjectResponse, error) {
	return &azdext.GetProjectResponse{Project: &azdext.ProjectConfig{}}, nil
}

type previewEnvServer struct {
	azdext.UnimplementedEnvironmentServiceServer
	selected string
}

func (s *previewEnvServer) GetValues(
	_ context.Context, request *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	s.selected = request.Name
	if request.Name == "" {
		return nil, status.Error(codes.Unknown, "default environment not found")
	}
	return &azdext.KeyValueListResponse{}, nil
}

var previewFeaturesOn = map[string]any{"agents": map[string]any{
	"managed_harness": "on", "voice": "on", "long_running": "on",
}}

func registerPreviewFeaturesOn(server *grpc.Server) {
	azdext.RegisterUserConfigServiceServer(server, &previewConfigServer{alpha: previewFeaturesOn})
	azdext.RegisterEnvironmentServiceServer(server, &previewEnvServer{})
}

func newPreviewFeatureClient(t *testing.T, alpha map[string]any) (*azdext.AzdClient, *previewEnvServer) {
	t.Helper()
	server := grpc.NewServer()
	env := &previewEnvServer{}
	azdext.RegisterUserConfigServiceServer(server, &previewConfigServer{alpha: alpha})
	azdext.RegisterProjectServiceServer(server, &previewProjectServer{})
	azdext.RegisterEnvironmentServiceServer(server, env)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return client, env
}

func TestPreviewNestedHarnessReference(t *testing.T) {
	t.Setenv(featureflags.ManagedHarness.EnvVar, "")
	t.Setenv("AZD_ENVIRONMENT", "")
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "harness.yaml"),
		[]byte("type: microsoft_copilot_preview\n"), 0600))
	service := &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost,
		AdditionalProperties: mustStruct(t, map[string]any{
			"kind": "prompt", "harness": map[string]any{"$ref": "harness.yaml"},
		})}
	before := proto.CloneOf(service)
	client, _ := newPreviewFeatureClient(t, nil)
	require.ErrorContains(t, RequireAgentFeatures(t.Context(), client, service, root), "agents.managed_harness")
	provider := &AgentServiceTargetProvider{azdClient: client, projectPath: root}
	_, err := provider.Package(t.Context(), service, nil, nil)
	require.ErrorContains(t, err, "agents.managed_harness")
	require.True(t, proto.Equal(before, service))
}

func TestPreviewFeatureRuntimeBoundaries(t *testing.T) {
	for _, feature := range featureflags.All() {
		t.Setenv(feature.EnvVar, "")
	}
	t.Setenv("AZD_ENVIRONMENT", "")
	client, env := newPreviewFeatureClient(t, nil)
	for _, tc := range []struct {
		name       string
		definition map[string]any
		feature    featureflags.Feature
	}{
		{"ordinary hosted", map[string]any{"kind": "hosted"}, featureflags.Feature{}},
		{"ordinary prompt", map[string]any{"kind": "prompt"}, featureflags.Feature{}},
		{"managed harness", map[string]any{"kind": "prompt",
			"harness": map[string]any{"type": "microsoft_copilot_preview"}}, featureflags.ManagedHarness},
		{"legacy harness", map[string]any{"kind": "prompt",
			"harness": map[string]any{"type": "github_copilot_preview"}}, featureflags.ManagedHarness},
		{"managed harness with whitespace", map[string]any{
			"kind": "prompt", "name": "agent", "model": "gpt-4.1-mini", "instructions": "Be helpful.",
			"harness": map[string]any{"type": " \tmicrosoft_copilot_preview\r\n "},
		}, featureflags.ManagedHarness},
		{"legacy harness with whitespace", map[string]any{
			"kind": "prompt", "name": "agent", "model": "gpt-4.1-mini", "instructions": "Be helpful.",
			"harness": map[string]any{"type": " \tgithub_copilot_preview\r\n "},
		}, featureflags.ManagedHarness},
		{"voice", map[string]any{"kind": "voice"}, featureflags.Voice},
		{"prompt voice", map[string]any{"kind": "prompt-voice"}, featureflags.Voice},
		{"hosted voice bridge", map[string]any{"kind": "hosted",
			"protocols": []any{map[string]any{"protocol": "invocations_ws"}}}, featureflags.Voice},
		{"api hosted voice bridge", map[string]any{"kind": "hosted",
			"protocol_versions": []any{map[string]any{"protocol": "invocations_ws"}}}, featureflags.Voice},
		{"legacy api hosted voice bridge", map[string]any{"kind": "hosted",
			"container_protocol_versions": []any{map[string]any{"protocol": " invocations_ws "}}}, featureflags.Voice},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			props, err := structpb.NewStruct(tc.definition)
			require.NoError(t, err)
			for _, ref := range []bool{false, true} {
				t.Run(map[bool]string{false: "inline", true: "ref"}[ref], func(t *testing.T) {
					service := &azdext.ServiceConfig{Name: "agent", Host: foundryAgentHost, AdditionalProperties: props}
					if ref {
						content, err := json.Marshal(tc.definition)
						require.NoError(t, err)
						require.NoError(t, os.WriteFile(filepath.Join(root, "agent.json"), content, 0600))
						service.AdditionalProperties = mustStruct(t, map[string]any{"$ref": "agent.json"})
					}
					before := proto.CloneOf(service)
					ctx := featureflags.WithEnvironment(t.Context(), "selected")
					err := RequireAgentFeatures(ctx, client, service, root)
					if tc.feature.ID == "" {
						require.NoError(t, err)
						require.NoError(t, RequireAgentFeatures(ctx, nil, service, root))
					} else {
						require.ErrorContains(t, err, tc.feature.ID)
						require.Equal(t, "selected", env.selected)
						require.Contains(t, err.Error(), "disabled")
						provider := &AgentServiceTargetProvider{azdClient: client, projectPath: root}
						_, err = provider.Package(ctx, proto.CloneOf(service), nil, nil)
						require.ErrorContains(t, err, tc.feature.ID)
						_, err = provider.Deploy(ctx, proto.CloneOf(service), nil, nil, nil)
						require.ErrorContains(t, err, tc.feature.ID)
						_, err = provider.Publish(ctx, proto.CloneOf(service), nil, nil, nil, nil)
						require.ErrorContains(t, err, tc.feature.ID)
						_, err = provider.GetTargetResource(ctx, "", proto.CloneOf(service), nil)
						require.ErrorContains(t, err, tc.feature.ID)
						t.Setenv(tc.feature.EnvVar, "true")
						require.NoError(t, RequireAgentFeatures(ctx, client, service, root))
						t.Setenv(tc.feature.EnvVar, "")
					}
					require.True(t, proto.Equal(before, service))
				})
			}
		})
	}
}
