// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

type selfInitializingProjectServer struct {
	azdext.UnimplementedProjectServiceServer
	project       *azdext.ProjectConfig
	services      map[string]map[string]any
	setConfigErr  error
	providerSet   bool
	deploymentSet bool
}

func (s *selfInitializingProjectServer) Get(
	context.Context,
	*azdext.EmptyRequest,
) (*azdext.GetProjectResponse, error) {
	return &azdext.GetProjectResponse{Project: s.project}, nil
}

func (s *selfInitializingProjectServer) GetConfigSection(
	_ context.Context,
	request *azdext.GetProjectConfigSectionRequest,
) (*azdext.GetProjectConfigSectionResponse, error) {
	if request.GetPath() != "services" {
		return &azdext.GetProjectConfigSectionResponse{}, nil
	}
	services := make(map[string]any, len(s.services))
	for name, body := range s.services {
		services[name] = body
	}
	section, err := structpb.NewStruct(services)
	if err != nil {
		return nil, err
	}
	return &azdext.GetProjectConfigSectionResponse{
		Section: section,
		Found:   len(services) > 0,
	}, nil
}

func (s *selfInitializingProjectServer) AddService(
	_ context.Context,
	request *azdext.AddServiceRequest,
) (*azdext.EmptyResponse, error) {
	service := request.GetService()
	name := service.GetName()
	s.project.Services[name] = &azdext.ServiceConfig{
		Name: name,
		Host: service.GetHost(),
	}
	if _, ok := s.services[name]; !ok {
		s.services[name] = map[string]any{}
	}
	return &azdext.EmptyResponse{}, nil
}

func (s *selfInitializingProjectServer) SetConfigValue(
	_ context.Context,
	_ *azdext.SetProjectConfigValueRequest,
) (*azdext.EmptyResponse, error) {
	if s.setConfigErr != nil {
		return nil, s.setConfigErr
	}
	s.providerSet = true
	return &azdext.EmptyResponse{}, nil
}

func (s *selfInitializingProjectServer) SetServiceConfigValue(
	_ context.Context,
	request *azdext.SetServiceConfigValueRequest,
) (*azdext.EmptyResponse, error) {
	body := s.services[request.GetServiceName()]
	if body == nil {
		body = map[string]any{}
		s.services[request.GetServiceName()] = body
	}
	body[request.GetPath()] = request.GetValue().AsInterface()
	if request.GetPath() == "deployments" {
		s.deploymentSet = true
	}
	return &azdext.EmptyResponse{}, nil
}

func (s *selfInitializingProjectServer) SetServiceConfigSection(
	_ context.Context,
	request *azdext.SetServiceConfigSectionRequest,
) (*azdext.EmptyResponse, error) {
	s.services[request.GetServiceName()] = request.GetSection().AsMap()
	return &azdext.EmptyResponse{}, nil
}

func (s *selfInitializingProjectServer) UnsetConfig(
	_ context.Context,
	request *azdext.UnsetProjectConfigRequest,
) (*azdext.EmptyResponse, error) {
	const prefix = "services."
	path := request.GetPath()
	if len(path) > len(prefix) && path[:len(prefix)] == prefix {
		name := path[len(prefix):]
		delete(s.services, name)
		delete(s.project.Services, name)
	}
	return &azdext.EmptyResponse{}, nil
}

type selfInitializingEnvironmentServer struct {
	azdext.UnimplementedEnvironmentServiceServer
	current string
	values  map[string]string
}

func (s *selfInitializingEnvironmentServer) GetCurrent(
	context.Context,
	*azdext.EmptyRequest,
) (*azdext.EnvironmentResponse, error) {
	return &azdext.EnvironmentResponse{
		Environment: &azdext.Environment{Name: s.current},
	}, nil
}

func (s *selfInitializingEnvironmentServer) Select(
	_ context.Context,
	request *azdext.SelectEnvironmentRequest,
) (*azdext.EmptyResponse, error) {
	s.current = request.GetName()
	return &azdext.EmptyResponse{}, nil
}

func (s *selfInitializingEnvironmentServer) GetValues(
	_ context.Context,
	_ *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	response := &azdext.KeyValueListResponse{}
	for key, value := range s.values {
		response.KeyValues = append(response.KeyValues, &azdext.KeyValue{
			Key:   key,
			Value: value,
		})
	}
	return response, nil
}

func (s *selfInitializingEnvironmentServer) SetValue(
	_ context.Context,
	request *azdext.SetEnvRequest,
) (*azdext.EmptyResponse, error) {
	s.values[request.GetKey()] = request.GetValue()
	return &azdext.EmptyResponse{}, nil
}

type selfInitializingAIService struct {
	azdext.UnimplementedAiModelServiceServer
	calls int
}

type selfInitializingWorkflowServer struct {
	azdext.UnimplementedWorkflowServiceServer
	root string
	args [][]string
}

func (s *selfInitializingWorkflowServer) Run(
	_ context.Context,
	request *azdext.RunWorkflowRequest,
) (*azdext.EmptyResponse, error) {
	workflow := request.GetWorkflow()
	if workflow != nil && len(workflow.GetSteps()) > 0 {
		command := workflow.GetSteps()[0].GetCommand()
		if command != nil {
			s.args = append(s.args, append([]string(nil), command.GetArgs()...))
		}
	}
	if s.root != "" {
		if err := os.WriteFile(
			filepath.Join(s.root, "azure.yaml"),
			[]byte("name: test\n"),
			0600,
		); err != nil {
			return nil, err
		}
	}
	return &azdext.EmptyResponse{}, nil
}

func (s *selfInitializingAIService) ResolveModelDeployments(
	_ context.Context,
	_ *azdext.ResolveModelDeploymentsRequest,
) (*azdext.ResolveModelDeploymentsResponse, error) {
	s.calls++
	return &azdext.ResolveModelDeploymentsResponse{
		Deployments: []*azdext.AiModelDeployment{{
			ModelName: "gpt-4.1",
			Format:    "OpenAI",
			Version:   "2025-04-14",
			Location:  "eastus",
			Sku: &azdext.AiModelSku{
				Name:            "GlobalStandard",
				DefaultCapacity: 1,
			},
			Capacity: 1,
		}},
	}, nil
}

func newSelfInitializingDeploymentClient(
	t *testing.T,
	root string,
) (
	*azdext.AzdClient,
	*selfInitializingProjectServer,
	*selfInitializingEnvironmentServer,
	*selfInitializingAIService,
	*selfInitializingWorkflowServer,
) {
	t.Helper()
	projectServer := &selfInitializingProjectServer{
		project: &azdext.ProjectConfig{
			Name:     "test",
			Path:     root,
			Services: map[string]*azdext.ServiceConfig{},
		},
		services: map[string]map[string]any{},
	}
	environmentServer := &selfInitializingEnvironmentServer{
		current: "test",
		values: map[string]string{
			"AZURE_SUBSCRIPTION_ID": "subscription",
			"AZURE_TENANT_ID":       "tenant",
			"AZURE_LOCATION":        "eastus",
		},
	}
	aiServer := &selfInitializingAIService{}
	workflowServer := &selfInitializingWorkflowServer{root: root}

	server := grpc.NewServer()
	azdext.RegisterProjectServiceServer(server, projectServer)
	azdext.RegisterEnvironmentServiceServer(server, environmentServer)
	azdext.RegisterAiModelServiceServer(server, aiServer)
	azdext.RegisterWorkflowServiceServer(server, workflowServer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	client, err := azdext.NewAzdClient(
		azdext.WithAddress(listener.Addr().String()),
	)
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return client, projectServer, environmentServer, aiServer, workflowServer
}

func TestEnsureProjectInitializesEmptyDirectory(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("AZD_EXEC_PROJECT_DIR", root)

	client, _, _, _, workflowServer := newSelfInitializingDeploymentClient(t, root)

	project, initialized, err := ensureProject(t.Context(), client, root)
	require.NoError(t, err)
	require.NotNil(t, project)
	assert.True(t, initialized)
	assert.Equal(t, root, project.GetPath())
	assert.FileExists(t, filepath.Join(root, "azure.yaml"))
	require.Len(t, workflowServer.args, 1)
	assert.Equal(
		t,
		[]string{
			"init", "--minimal", "--no-prompt",
			"--environment", deriveProjectEnvironmentName(root),
		},
		workflowServer.args[0],
	)
}

func TestProjectDeploymentAddInitializesMissingProjectService(t *testing.T) {
	tests := []struct {
		name          string
		delegated     bool
		requestJSON   string
		expectedModel string
		expectedName  string
	}{
		{
			name:          "direct",
			expectedModel: "gpt-4.1",
			expectedName:  "gpt-4.1",
		},
		{
			name:      "delegated",
			delegated: true,
			requestJSON: `{
  "schemaVersion": 1,
  "source": "azure.ai.agents/init",
  "sourceVersion": "1.0.0",
  "model": {"name": "gpt-4.1", "deploymentName": "chat"},
  "setAsDefault": true
}`,
			expectedModel: "gpt-4.1",
			expectedName:  "chat",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			t.Setenv("AZD_EXEC_PROJECT_DIR", root)

			client, projectServer, environmentServer, aiServer, workflowServer :=
				newSelfInitializingDeploymentClient(t, root)
			flags := &projectDeploymentFlags{
				model:  test.expectedModel,
				output: "none",
			}
			if test.delegated {
				requestPath := filepath.Join(root, "request.json")
				require.NoError(t, os.WriteFile(
					requestPath, []byte(test.requestJSON), 0600,
				))
				flags.model = ""
				flags.requestFile = requestPath
			}
			action := &ProjectDeploymentAddAction{
				client: client,
				flags:  flags,
				extCtx: &azdext.ExtensionContext{
					Environment:  "test",
					NoPrompt:     true,
					OutputFormat: "none",
				},
			}

			require.NoError(t, action.Run(t.Context()))
			require.Len(t, workflowServer.args, 1)
			assert.Equal(
				t,
				[]string{
					"init", "--minimal", "--no-prompt",
					"--environment", deriveProjectEnvironmentName(root),
				},
				workflowServer.args[0],
			)
			require.Contains(t, projectServer.project.Services, "test")
			assert.True(t, projectServer.providerSet)
			assert.True(t, projectServer.deploymentSet)
			assert.Equal(t, 1, aiServer.calls)
			assert.Equal(
				t,
				test.expectedName,
				environmentServer.values["AZURE_AI_MODEL_DEPLOYMENT_NAME"],
			)
			assert.Contains(
				t,
				projectServer.services["test"],
				"deployments",
			)
		})
	}
}

func TestProjectDeploymentAddStopsWhenProjectSetupFails(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("AZD_EXEC_PROJECT_DIR", root)
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "azure.yaml"),
		[]byte("name: test\n"),
		0600,
	))

	client, projectServer, _, aiServer, _ :=
		newSelfInitializingDeploymentClient(t, root)
	projectServer.setConfigErr = errors.New("project configuration write failed")
	action := &ProjectDeploymentAddAction{
		client: client,
		flags: &projectDeploymentFlags{
			model:  "gpt-4.1",
			output: "none",
		},
		extCtx: &azdext.ExtensionContext{
			Environment:  "test",
			NoPrompt:     true,
			OutputFormat: "none",
		},
	}

	require.Error(t, action.Run(t.Context()))
	assert.False(t, projectServer.providerSet)
	assert.False(t, projectServer.deploymentSet)
	assert.Empty(t, projectServer.services)
	assert.Equal(t, 0, aiServer.calls)
}
