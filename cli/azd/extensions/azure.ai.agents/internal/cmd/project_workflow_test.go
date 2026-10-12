// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"testing"

	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type recordingProjectWorkflowServer struct {
	azdext.UnimplementedWorkflowServiceServer

	mu       sync.Mutex
	requests []*azdext.RunWorkflowRequest
	err      error
	runHook  func(int) error
}

func (s *recordingProjectWorkflowServer) Run(
	_ context.Context,
	req *azdext.RunWorkflowRequest,
) (*azdext.EmptyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, req)
	if s.runHook != nil {
		if err := s.runHook(len(s.requests)); err != nil {
			return nil, err
		}
	}
	if s.err != nil {
		return nil, s.err
	}
	return &azdext.EmptyResponse{}, nil
}

func newProjectWorkflowClient(
	t *testing.T,
	projectServer azdext.ProjectServiceServer,
	workflowServer azdext.WorkflowServiceServer,
) *azdext.AzdClient {
	t.Helper()

	grpcServer := grpc.NewServer()
	azdext.RegisterProjectServiceServer(grpcServer, projectServer)
	azdext.RegisterWorkflowServiceServer(grpcServer, workflowServer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() {
		_ = grpcServer.Serve(listener)
	}()
	t.Cleanup(func() {
		grpcServer.Stop()
		_ = listener.Close()
	})

	client, err := azdext.NewAzdClient(
		azdext.WithAddress(listener.Addr().String()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	return client
}

func workflowArgs(
	t *testing.T,
	server *recordingProjectWorkflowServer,
	index int,
) []string {
	t.Helper()
	server.mu.Lock()
	defer server.mu.Unlock()
	require.Greater(t, len(server.requests), index)
	require.Len(t, server.requests[index].Workflow.Steps, 1)
	return server.requests[index].Workflow.Steps[0].Command.Args
}

func expectedProjectWorkflowArgs(
	t *testing.T,
	projectRoot string,
	args ...string,
) []string {
	t.Helper()
	projectRoot, err := filepath.Abs(projectRoot)
	require.NoError(t, err)
	return append(args, "--cwd", projectRoot)
}

func TestAuthorFoundryProjectUsesPublicCommand(t *testing.T) {
	t.Parallel()

	projectServer := &recordingProjectServer{
		existing: map[string]*azdext.ServiceConfig{},
	}
	workflowServer := &recordingProjectWorkflowServer{}
	client := newProjectWorkflowClient(t, projectServer, workflowServer)

	target := &FoundryProjectInfo{
		ResourceId: "/subscriptions/sub/resourceGroups/rg/providers/" +
			"Microsoft.CognitiveServices/accounts/account/projects/project",
	}
	projectRoot := t.TempDir()
	require.NoError(t, authorFoundryProject(
		t.Context(), client, target, projectRoot, projectAuthoringExisting, true,
	))

	assert.Equal(t, expectedProjectWorkflowArgs(t, projectRoot,
		"ai",
		"project",
		"add",
		"--no-prompt",
		"--output",
		"none",
		"--project-id",
		target.ResourceId,
		"--force",
	), workflowArgs(t, workflowServer, 0))
	assert.Empty(t, projectServer.added)
}

func TestAuthorFoundryProjectUsesEndpointFlag(t *testing.T) {
	t.Parallel()

	projectServer := &recordingProjectServer{
		existing: map[string]*azdext.ServiceConfig{},
	}
	workflowServer := &recordingProjectWorkflowServer{}
	client := newProjectWorkflowClient(t, projectServer, workflowServer)
	target := &FoundryProjectInfo{
		AccountName: "account",
		ProjectName: "project",
	}

	projectRoot := t.TempDir()
	require.NoError(t, authorFoundryProject(
		t.Context(), client, target, projectRoot, projectAuthoringExisting, true,
	))

	assert.Equal(t, expectedProjectWorkflowArgs(t, projectRoot,
		"ai",
		"project",
		"add",
		"--no-prompt",
		"--output",
		"none",
		"--project-endpoint",
		target.Endpoint(),
		"--force",
	), workflowArgs(t, workflowServer, 0))
	assert.Empty(t, projectServer.added)
}

func TestAuthorFoundryProjectPreservesPromptMode(t *testing.T) {
	t.Parallel()

	workflowServer := &recordingProjectWorkflowServer{}
	client := newProjectWorkflowClient(
		t,
		&recordingProjectServer{existing: map[string]*azdext.ServiceConfig{}},
		workflowServer,
	)
	target := &FoundryProjectInfo{AccountName: "account", ProjectName: "project"}
	projectRoot := t.TempDir()

	require.NoError(t, authorFoundryProject(
		t.Context(), client, target, projectRoot, projectAuthoringExisting, false,
	))

	assert.Equal(t, expectedProjectWorkflowArgs(t, projectRoot,
		"ai", "project", "add", "--output", "none",
		"--project-endpoint", target.Endpoint(), "--force",
	), workflowArgs(t, workflowServer, 0))
}

func TestAuthorFoundryProjectUsesNewProjectFlag(t *testing.T) {
	t.Parallel()

	workflowServer := &recordingProjectWorkflowServer{}
	client := newProjectWorkflowClient(
		t,
		&recordingProjectServer{
			existing: map[string]*azdext.ServiceConfig{},
		},
		workflowServer,
	)
	projectRoot := t.TempDir()

	require.NoError(t, authorFoundryProject(
		t.Context(), client, nil, projectRoot, projectAuthoringNew, true,
	))

	assert.Equal(t, expectedProjectWorkflowArgs(t, projectRoot,
		"ai",
		"project",
		"add",
		"--no-prompt",
		"--output",
		"none",
		"--new-project",
	), workflowArgs(t, workflowServer, 0))
}

func TestAuthorNewFoundryProjectPreservesSelection(t *testing.T) {
	t.Parallel()

	envServer := &testEnvironmentServiceServer{
		values: map[string]map[string]string{
			"test": {
				"AZURE_AI_PROJECT_NAME":         "new-project",
				"AZURE_RESOURCE_GROUP":          "new-rg",
				"AZURE_LOCATION":                "eastus",
				"AZURE_AI_DEPLOYMENTS_LOCATION": "eastus",
			},
		},
	}
	workflowServer := &recordingProjectWorkflowServer{}
	client := newTestAzdClient(t, envServer, workflowServer)
	projectRoot := t.TempDir()

	require.NoError(t, authorNewFoundryProject(
		t.Context(),
		client,
		"test",
		projectRoot,
	))

	assert.Equal(t, expectedProjectWorkflowArgs(t, projectRoot,
		"ai",
		"project",
		"add",
		"--no-prompt",
		"--output",
		"none",
		"--new-project",
	), workflowArgs(t, workflowServer, 0))
	assert.Equal(t, "new-project", envServer.values["test"]["AZURE_AI_PROJECT_NAME"])
	assert.Equal(t, "new-rg", envServer.values["test"]["AZURE_RESOURCE_GROUP"])
	assert.Equal(t, "eastus", envServer.values["test"]["AZURE_LOCATION"])
	assert.Equal(
		t,
		"eastus",
		envServer.values["test"]["AZURE_AI_DEPLOYMENTS_LOCATION"],
	)
}

func TestAuthorNewFoundryProjectRestoresValuesAfterProjectsMutation(t *testing.T) {
	t.Parallel()

	envServer := &testEnvironmentServiceServer{
		values: map[string]map[string]string{
			"test": {
				"AZURE_AI_PROJECT_NAME":         "new-project",
				"AZURE_RESOURCE_GROUP":          "new-rg",
				"AZURE_AI_ACCOUNT_NAME":         "new-account",
				"AZURE_LOCATION":                "eastus",
				"AZURE_AI_DEPLOYMENTS_LOCATION": "eastus",
			},
		},
	}
	workflowServer := &recordingProjectWorkflowServer{
		runHook: func(_ int) error {
			for _, key := range newProjectEnvironmentKeys {
				delete(envServer.values["test"], key)
			}
			return nil
		},
	}
	client := newTestAzdClient(t, envServer, workflowServer)

	require.NoError(t, authorNewFoundryProject(
		t.Context(), client, "test", t.TempDir(),
	))

	assert.Equal(t, "new-project", envServer.values["test"]["AZURE_AI_PROJECT_NAME"])
	assert.Equal(t, "new-rg", envServer.values["test"]["AZURE_RESOURCE_GROUP"])
	assert.Equal(t, "new-account", envServer.values["test"]["AZURE_AI_ACCOUNT_NAME"])
	assert.Equal(t, "eastus", envServer.values["test"]["AZURE_LOCATION"])
	assert.Equal(
		t,
		"eastus",
		envServer.values["test"]["AZURE_AI_DEPLOYMENTS_LOCATION"],
	)
}

func TestAuthorNewFoundryProjectRestoresValuesAfterCancellation(t *testing.T) {
	t.Parallel()

	envServer := &testEnvironmentServiceServer{
		values: map[string]map[string]string{
			"test": {
				"AZURE_AI_PROJECT_NAME": "new-project",
			},
		},
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	workflowServer := &recordingProjectWorkflowServer{
		runHook: func(_ int) error {
			delete(envServer.values["test"], "AZURE_AI_PROJECT_NAME")
			cancel()
			return status.Error(codes.Canceled, "cancelled")
		},
	}
	client := newTestAzdClient(t, envServer, workflowServer)

	err := authorNewFoundryProject(ctx, client, "test", t.TempDir())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "was cancelled")
	assert.Equal(
		t,
		"new-project",
		envServer.values["test"]["AZURE_AI_PROJECT_NAME"],
	)
}

func TestAuthorFoundryDeploymentsUsesPublicCommand(t *testing.T) {
	t.Parallel()

	projectServer := &recordingProjectServer{
		existing: map[string]*azdext.ServiceConfig{},
	}
	workflowServer := &recordingProjectWorkflowServer{}
	client := newProjectWorkflowClient(t, projectServer, workflowServer)
	deployment := project.Deployment{
		Name: "gpt-4o",
		Model: project.DeploymentModel{
			Format:  "AzureOpenAI",
			Name:    "gpt-4o",
			Version: "2024-08-06",
		},
		Sku: project.DeploymentSku{
			Name:     "GlobalStandard",
			Capacity: 50,
		},
	}

	projectRoot := t.TempDir()
	require.NoError(t, authorFoundryDeployments(
		t.Context(),
		client,
		projectRoot,
		[]project.Deployment{deployment},
	))

	assert.Equal(t, expectedProjectWorkflowArgs(
		t, projectRoot,
		"ai",
		"project",
		"deployment",
		"add",
		"--no-prompt",
		"--output",
		"none",
		"--model",
		"AzureOpenAI/gpt-4o",
		"--name",
		"gpt-4o",
		"--version",
		"2024-08-06",
		"--sku",
		"GlobalStandard",
		"--capacity",
		"50",
	), workflowArgs(t, workflowServer, 0))
	assert.Empty(t, projectServer.added)
}

func TestConfigureAdoptedModelUsesPublicCommand(t *testing.T) {
	t.Parallel()

	workflowServer := &recordingProjectWorkflowServer{}
	client := newProjectWorkflowClient(
		t,
		&recordingProjectServer{
			existing: map[string]*azdext.ServiceConfig{},
		},
		workflowServer,
	)
	projectRoot := t.TempDir()

	require.NoError(t, configureAdoptedModel(
		t.Context(),
		client,
		projectRoot,
		&initFlags{model: "gpt-4.1"},
	))

	assert.Equal(t, expectedProjectWorkflowArgs(
		t,
		projectRoot,
		"ai",
		"project",
		"deployment",
		"add",
		"--no-prompt",
		"--output",
		"none",
		"--model",
		"gpt-4.1",
	), workflowArgs(t, workflowServer, 0))
}

func TestConfigureAdoptedModelDeploymentTakesPrecedence(t *testing.T) {
	t.Parallel()

	workflowServer := &recordingProjectWorkflowServer{}
	client := newProjectWorkflowClient(
		t,
		&recordingProjectServer{
			existing: map[string]*azdext.ServiceConfig{},
		},
		workflowServer,
	)

	require.NoError(t, configureAdoptedModel(
		t.Context(),
		client,
		t.TempDir(),
		&initFlags{
			model:           "gpt-4.1",
			modelDeployment: "existing",
		},
	))

	assert.Empty(t, workflowServer.requests)
}

func TestShouldDeferAdoptedModelAuthoring(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		flags        *initFlags
		azureContext *azdext.AzureContext
		wantDefer    bool
	}{
		{
			name:  "missing context in no-prompt mode",
			flags: &initFlags{noPrompt: true},
			azureContext: &azdext.AzureContext{
				Scope: &azdext.AzureScope{},
			},
			wantDefer: true,
		},
		{
			name:  "complete context",
			flags: &initFlags{noPrompt: true},
			azureContext: &azdext.AzureContext{Scope: &azdext.AzureScope{
				SubscriptionId: "subscription-id",
				Location:       "eastus2",
			}},
		},
		{
			name: "explicit project id",
			flags: &initFlags{
				noPrompt: true,
				projectResourceId: "/subscriptions/sub/resourceGroups/rg/providers/" +
					"Microsoft.CognitiveServices/accounts/account/projects/project",
			},
			azureContext: &azdext.AzureContext{
				Scope: &azdext.AzureScope{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(
				t,
				tt.wantDefer,
				shouldDeferAdoptedModelAuthoring(tt.flags, tt.azureContext),
			)
		})
	}
}

func TestAuthorFoundryDeploymentsPreservesDefault(t *testing.T) {
	t.Parallel()

	envServer := &testEnvironmentServiceServer{
		values: map[string]map[string]string{
			"test": {
				"AZURE_AI_MODEL_DEPLOYMENT_NAME": "first",
			},
		},
	}
	workflowServer := &recordingProjectWorkflowServer{}
	client := newTestAzdClient(t, envServer, workflowServer)

	require.NoError(t, authorFoundryDeploymentsPreservingDefault(
		t.Context(),
		client,
		"test",
		t.TempDir(),
		[]project.Deployment{
			{Name: "first", Model: project.DeploymentModel{Name: "first"}},
			{Name: "second", Model: project.DeploymentModel{Name: "second"}},
		},
	))

	assert.Equal(
		t,
		"first",
		envServer.values["test"]["AZURE_AI_MODEL_DEPLOYMENT_NAME"],
	)
	assert.Len(t, workflowServer.requests, 2)
}

func TestAuthorFoundryDeploymentsRestoresDefaultAfterCancellation(t *testing.T) {
	t.Parallel()

	envServer := &testEnvironmentServiceServer{
		values: map[string]map[string]string{
			"test": {
				"AZURE_AI_MODEL_DEPLOYMENT_NAME": "first",
			},
		},
	}
	ctx, cancel := context.WithCancel(t.Context())
	workflowServer := &recordingProjectWorkflowServer{
		runHook: func(call int) error {
			if call != 2 {
				return nil
			}
			envServer.values["test"]["AZURE_AI_MODEL_DEPLOYMENT_NAME"] = "second"
			cancel()
			return status.Error(codes.Canceled, "cancelled")
		},
	}
	client := newTestAzdClient(t, envServer, workflowServer)

	err := authorFoundryDeploymentsPreservingDefault(
		ctx,
		client,
		"test",
		t.TempDir(),
		[]project.Deployment{
			{Name: "first", Model: project.DeploymentModel{Name: "first"}},
			{Name: "second", Model: project.DeploymentModel{Name: "second"}},
		},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "was cancelled")
	assert.Equal(
		t,
		"first",
		envServer.values["test"]["AZURE_AI_MODEL_DEPLOYMENT_NAME"],
	)
}

func TestProjectWorkflowPropagatesFailures(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("projects extension failed")
	workflowServer := &recordingProjectWorkflowServer{err: sentinel}
	client := newProjectWorkflowClient(
		t,
		&recordingProjectServer{existing: map[string]*azdext.ServiceConfig{}},
		workflowServer,
	)

	err := authorFoundryProject(
		t.Context(), client, nil, t.TempDir(), projectAuthoringCurrent, true,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "authoring the Foundry project failed")
	assert.Contains(t, err.Error(), sentinel.Error())
}

func TestProjectWorkflowPreservesCancellation(t *testing.T) {
	t.Parallel()

	workflowServer := &recordingProjectWorkflowServer{
		err: status.Error(codes.Canceled, "cancelled"),
	}
	client := newProjectWorkflowClient(
		t,
		&recordingProjectServer{existing: map[string]*azdext.ServiceConfig{}},
		workflowServer,
	)

	err := authorFoundryProject(
		t.Context(), client, nil, t.TempDir(), projectAuthoringCurrent, true,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "authoring the Foundry project was cancelled")
}

func TestAuthorCurrentFoundryProjectPreservesDeferredValues(t *testing.T) {
	t.Parallel()

	envServer := &testEnvironmentServiceServer{
		values: map[string]map[string]string{
			"test": {
				"AZURE_AI_PROJECT_NAME":         "deferred-project",
				"AZURE_RESOURCE_GROUP":          "deferred-rg",
				"AZURE_AI_ACCOUNT_NAME":         "deferred-account",
				"AZURE_LOCATION":                "eastus",
				"AZURE_AI_DEPLOYMENTS_LOCATION": "westus",
			},
		},
	}
	workflowServer := &recordingProjectWorkflowServer{
		runHook: func(_ int) error {
			for _, key := range newProjectEnvironmentKeys {
				delete(envServer.values["test"], key)
			}
			return nil
		},
	}
	client := newTestAzdClient(t, envServer, workflowServer)

	require.NoError(t, authorSelectedFoundryProject(
		t.Context(), client, "test", nil, t.TempDir(), projectAuthoringCurrent, true,
	))

	assert.Equal(t, "deferred-project",
		envServer.values["test"]["AZURE_AI_PROJECT_NAME"])
	assert.Equal(t, "deferred-rg",
		envServer.values["test"]["AZURE_RESOURCE_GROUP"])
	assert.Equal(t, "deferred-account",
		envServer.values["test"]["AZURE_AI_ACCOUNT_NAME"])
	assert.Equal(t, "eastus", envServer.values["test"]["AZURE_LOCATION"])
	assert.Equal(t, "westus",
		envServer.values["test"]["AZURE_AI_DEPLOYMENTS_LOCATION"])
}
