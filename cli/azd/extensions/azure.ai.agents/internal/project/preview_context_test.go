// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"net"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

type previewContextProjectServer struct {
	azdext.UnimplementedProjectServiceServer
	project *azdext.ProjectConfig
}

func (s *previewContextProjectServer) Get(context.Context, *azdext.EmptyRequest) (*azdext.GetProjectResponse, error) {
	return &azdext.GetProjectResponse{Project: s.project}, nil
}

type previewContextEnvironmentServer struct {
	azdext.UnimplementedEnvironmentServiceServer
	values     map[string]string
	currentErr error
	valuesErr  error
	writes     int
}

func (s *previewContextEnvironmentServer) GetCurrent(
	context.Context, *azdext.EmptyRequest,
) (*azdext.EnvironmentResponse, error) {
	if s.currentErr != nil {
		return nil, s.currentErr
	}
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: "test"}}, nil
}

func (s *previewContextEnvironmentServer) GetValues(
	context.Context, *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	if s.valuesErr != nil {
		return nil, s.valuesErr
	}
	result := &azdext.KeyValueListResponse{}
	for name, value := range s.values {
		result.KeyValues = append(result.KeyValues, &azdext.KeyValue{Key: name, Value: value})
	}
	return result, nil
}

func (s *previewContextEnvironmentServer) SetValue(
	context.Context, *azdext.SetEnvRequest,
) (*azdext.EmptyResponse, error) {
	s.writes++
	return nil, status.Error(codes.Internal, "preview must not write environment state")
}

func newPreviewContextClient(
	t *testing.T, project *azdext.ProjectConfig, environment *previewContextEnvironmentServer,
) *azdext.AzdClient {
	t.Helper()
	server := grpc.NewServer()
	azdext.RegisterProjectServiceServer(server, &previewContextProjectServer{project: project})
	azdext.RegisterEnvironmentServiceServer(server, environment)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	return client
}

func TestLoadServicePreviewOptionsUsesSelectedProject(t *testing.T) {
	t.Parallel()
	fixture := agentServicePreviewFixture(t)
	fixture.Service.Uses = []string{"ai-project"}
	projectProps, err := structpb.NewStruct(map[string]any{
		"endpoint": "https://account.services.ai.azure.com/api/projects/project/",
	})
	require.NoError(t, err)
	config := &azdext.ProjectConfig{
		Name: "project", Path: fixture.ProjectRoot, Services: map[string]*azdext.ServiceConfig{
			fixture.Service.Name: fixture.Service,
			"ai-project":         {Name: "ai-project", Host: "azure.ai.project", AdditionalProperties: projectProps},
		},
	}
	original := proto.CloneOf(config)
	env := &previewContextEnvironmentServer{values: map[string]string{
		"FOUNDRY_PROJECT_ENDPOINT": "https://other.services.ai.azure.com/api/projects/other",
	}}
	client := newPreviewContextClient(t, config, env)
	options, err := loadServicePreviewOptions(t.Context(), client, fixture.Service)
	require.NoError(t, err)
	assert.Same(t, fixture.Service, options.Service, "the core-selected service must not be reselected")
	assert.Equal(t, "https://account.services.ai.azure.com/api/projects/project", options.ProjectEndpoint)
	assert.Equal(t, "project", options.ProjectName)
	assert.Equal(t, "test", options.Environment["AZURE_ENV_NAME"])
	assert.Empty(t, options.Environment["AZURE_SUBSCRIPTION_ID"], "preview does not require provisioning outputs")
	assert.Equal(t, 0, env.writes)
	assert.True(t, proto.Equal(original, config))

	env.currentErr = status.Error(codes.NotFound, "not initialized")
	options, err = loadServicePreviewOptions(t.Context(), client, fixture.Service)
	require.NoError(t, err)
	assert.Empty(t, options.Environment)
	assert.Equal(t, "https://account.services.ai.azure.com/api/projects/project", options.ProjectEndpoint)
}

func TestLoadServicePreviewOptionsPropagatesEnvironmentErrors(t *testing.T) {
	t.Parallel()
	for _, env := range []*previewContextEnvironmentServer{
		{currentErr: status.Error(codes.PermissionDenied, "denied")},
		{valuesErr: status.Error(codes.Internal, "failed to read")},
	} {
		t.Run("", func(t *testing.T) {
			t.Parallel()
			fixture := agentServicePreviewFixture(t)
			config := &azdext.ProjectConfig{Path: fixture.ProjectRoot}
			client := newPreviewContextClient(t, config, env)
			_, err := loadServicePreviewOptions(t.Context(), client, fixture.Service)
			require.Error(t, err)
			assert.Zero(t, env.writes)
		})
	}
}

func TestPreviewServiceResponsePreservesStructuredData(t *testing.T) {
	t.Parallel()
	result := &DirectDeployPreviewResult{
		Name: "agent", Service: "service-key", Operation: "create_version", HasChanges: true,
		CurrentVersion: "2", SourcePath: filepath.Join("src", "agent"),
		Changes: []DeployPreviewChangeGroup{{Group: "environmentVariables", Changes: []DeployPreviewChange{
			{Field: "ANY_THING", Kind: "add", After: "[redacted]", Sensitive: true},
		}}},
		Image: &DeployPreviewImage{Mode: "code", Known: true},
	}
	response, err := servicePreviewResponse(result)
	require.NoError(t, err)
	assert.Contains(t, response.Message, "Preview: agent agent")
	assert.Contains(t, response.Message, "+ ANY_THING")
	assert.NotContains(t, response.Message, "Container image plan")
	assert.Equal(t, "agent", response.Data.Fields["name"].GetStringValue())
	assert.True(t, response.Data.Fields["hasChanges"].GetBoolValue())
	require.Len(t, response.Data.Fields["changes"].GetListValue().Values, 1)
	_, err = servicePreviewResponse(nil)
	require.Error(t, err)
}

func TestResolvePreviewProjectEndpointValidation(t *testing.T) {
	t.Parallel()
	service := &azdext.ServiceConfig{Name: "agent"}
	config := &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{}}
	endpoint, err := resolveAgentProjectEndpoint(service, config, map[string]string{
		"FOUNDRY_PROJECT_ENDPOINT": "https://account.services.ai.azure.com/api/projects/project",
	})
	require.NoError(t, err)
	assert.Equal(t, "https://account.services.ai.azure.com/api/projects/project", endpoint)
	service.Uses = []string{"missing"}
	_, err = resolveAgentProjectEndpoint(service, config, nil)
	require.ErrorContains(t, err, "unknown service")
}
