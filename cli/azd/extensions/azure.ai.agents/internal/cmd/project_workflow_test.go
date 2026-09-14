// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"net"
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
}

func (s *recordingProjectWorkflowServer) Run(
	_ context.Context,
	req *azdext.RunWorkflowRequest,
) (*azdext.EmptyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, req)
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
	require.NoError(t, authorFoundryProject(t.Context(), client, target))

	assert.Equal(t, []string{
		"ai",
		"project",
		"add",
		"--no-prompt",
		"--output",
		"none",
		"--project-id",
		target.ResourceId,
	}, workflowArgs(t, workflowServer, 0))
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

	require.NoError(t, authorFoundryProject(t.Context(), client, target))

	assert.Equal(t, []string{
		"ai",
		"project",
		"add",
		"--no-prompt",
		"--output",
		"none",
		"--project-endpoint",
		target.Endpoint(),
	}, workflowArgs(t, workflowServer, 0))
	assert.Empty(t, projectServer.added)
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
			Name:    "gpt-4o",
			Version: "2024-08-06",
		},
		Sku: project.DeploymentSku{
			Name:     "GlobalStandard",
			Capacity: 50,
		},
	}

	require.NoError(t, authorFoundryDeployments(
		t.Context(),
		client,
		[]project.Deployment{deployment},
	))

	assert.Equal(t, []string{
		"ai",
		"project",
		"deployment",
		"add",
		"--no-prompt",
		"--output",
		"none",
		"--model",
		"gpt-4o",
		"--name",
		"gpt-4o",
		"--version",
		"2024-08-06",
		"--sku",
		"GlobalStandard",
		"--capacity",
		"50",
	}, workflowArgs(t, workflowServer, 0))
	assert.Empty(t, projectServer.added)
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

func TestProjectWorkflowPropagatesFailures(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("projects extension failed")
	workflowServer := &recordingProjectWorkflowServer{err: sentinel}
	client := newProjectWorkflowClient(
		t,
		&recordingProjectServer{existing: map[string]*azdext.ServiceConfig{}},
		workflowServer,
	)

	err := authorFoundryProject(t.Context(), client, nil)
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

	err := authorFoundryProject(t.Context(), client, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "authoring the Foundry project was cancelled")
}
