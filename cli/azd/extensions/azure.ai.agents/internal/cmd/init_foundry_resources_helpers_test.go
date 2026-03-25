// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"net"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestExtractProjectDetails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		resourceId  string
		wantSub     string
		wantRG      string
		wantAccount string
		wantProject string
		wantErr     bool
	}{
		{
			name: "valid resource ID",
			resourceId: "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/my-rg/" +
				"providers/Microsoft.CognitiveServices/accounts/my-account/projects/my-project",
			wantSub:     "00000000-0000-0000-0000-000000000001",
			wantRG:      "my-rg",
			wantAccount: "my-account",
			wantProject: "my-project",
		},
		{
			name: "resource ID with special characters in names",
			resourceId: "/subscriptions/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/resourceGroups/rg-with-dashes/" +
				"providers/Microsoft.CognitiveServices/accounts/account_underscore/projects/proj.dots",
			wantSub:     "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			wantRG:      "rg-with-dashes",
			wantAccount: "account_underscore",
			wantProject: "proj.dots",
		},
		{
			name:       "empty string",
			resourceId: "",
			wantErr:    true,
		},
		{
			name:       "malformed - missing projects segment",
			resourceId: "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.CognitiveServices/accounts/acct1",
			wantErr:    true,
		},
		{
			name:       "malformed - wrong provider",
			resourceId: "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Storage/accounts/acct1/projects/proj1",
			wantErr:    true,
		},
		{
			name:       "malformed - extra trailing segment",
			resourceId: "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.CognitiveServices/accounts/acct1/projects/proj1/extra",
			wantErr:    true,
		},
		{
			name:       "malformed - random string",
			resourceId: "not-a-resource-id",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := extractProjectDetails(tt.resourceId)

			if tt.wantErr {
				require.Error(t, err)
				require.Nil(t, result)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, tt.wantSub, result.SubscriptionId)
			require.Equal(t, tt.wantRG, result.ResourceGroupName)
			require.Equal(t, tt.wantAccount, result.AccountName)
			require.Equal(t, tt.wantProject, result.ProjectName)
			require.Equal(t, tt.resourceId, result.ResourceId)
		})
	}
}

func TestFoundryProjectInfoResourceIdConstruction(t *testing.T) {
	t.Parallel()

	// Verify round-trip: parse a resource ID then reconstruct it
	originalId := "/subscriptions/aaaa/resourceGroups/rg-test/providers/Microsoft.CognitiveServices/accounts/acct-1/projects/proj-1"

	info, err := extractProjectDetails(originalId)
	require.NoError(t, err)

	reconstructed := "/subscriptions/" + info.SubscriptionId +
		"/resourceGroups/" + info.ResourceGroupName +
		"/providers/Microsoft.CognitiveServices/accounts/" + info.AccountName +
		"/projects/" + info.ProjectName

	require.Equal(t, originalId, reconstructed)
}

func TestCreateNewEnvironment_ReturnsExistingNamedEnvironment(t *testing.T) {
	t.Parallel()

	envServer := &testEnvironmentServiceServer{
		environments: map[string]*azdext.Environment{
			"agent-dev": {Name: "agent-dev"},
		},
	}
	workflowServer := &testWorkflowServiceServer{}
	azdClient := newTestAzdClient(t, envServer, workflowServer)

	env, err := createNewEnvironment(t.Context(), azdClient, "agent-dev")

	require.NoError(t, err)
	require.Equal(t, "agent-dev", env.Name)
	require.Equal(t, 0, workflowServer.runCalls)
}

func TestCreateNewEnvironment_ReusesExistingEnvironmentAfterAlreadyExistsError(t *testing.T) {
	t.Parallel()

	const envName = "agent-dev"

	envServer := &testEnvironmentServiceServer{
		environments: make(map[string]*azdext.Environment),
	}
	workflowServer := &testWorkflowServiceServer{
		runErr: status.Error(
			codes.AlreadyExists,
			"environment already exists",
		),
		runHook: func() {
			envServer.environments[envName] = &azdext.Environment{Name: envName}
		},
	}
	azdClient := newTestAzdClient(t, envServer, workflowServer)

	env, err := createNewEnvironment(t.Context(), azdClient, envName)

	require.NoError(t, err)
	require.Equal(t, envName, env.Name)
	require.Equal(t, 1, workflowServer.runCalls)
}

type testEnvironmentServiceServer struct {
	azdext.UnimplementedEnvironmentServiceServer
	environments map[string]*azdext.Environment
	current      *azdext.Environment
}

func (s *testEnvironmentServiceServer) GetCurrent(context.Context, *azdext.EmptyRequest) (*azdext.EnvironmentResponse, error) {
	if s.current == nil {
		return nil, status.Error(codes.NotFound, "current environment not found")
	}

	return &azdext.EnvironmentResponse{Environment: s.current}, nil
}

func (s *testEnvironmentServiceServer) Get(_ context.Context, req *azdext.GetEnvironmentRequest) (*azdext.EnvironmentResponse, error) {
	env, ok := s.environments[req.Name]
	if !ok {
		return nil, status.Error(codes.NotFound, "environment not found")
	}

	return &azdext.EnvironmentResponse{Environment: env}, nil
}

type testWorkflowServiceServer struct {
	azdext.UnimplementedWorkflowServiceServer
	runCalls int
	runErr   error
	runHook  func()
}

func (s *testWorkflowServiceServer) Run(context.Context, *azdext.RunWorkflowRequest) (*azdext.EmptyResponse, error) {
	s.runCalls++
	if s.runHook != nil {
		s.runHook()
	}
	if s.runErr != nil {
		return nil, s.runErr
	}

	return &azdext.EmptyResponse{}, nil
}

func newTestAzdClient(
	t *testing.T,
	envServer azdext.EnvironmentServiceServer,
	workflowServer azdext.WorkflowServiceServer,
) *azdext.AzdClient {
	t.Helper()

	grpcServer := grpc.NewServer()
	azdext.RegisterEnvironmentServiceServer(grpcServer, envServer)
	azdext.RegisterWorkflowServiceServer(grpcServer, workflowServer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	serveErr := make(chan error, 1)
	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			serveErr <- err
		}
	}()

	t.Cleanup(func() {
		grpcServer.Stop()
		_ = listener.Close()
		select {
		case err := <-serveErr:
			require.ErrorIs(t, err, grpc.ErrServerStopped)
		default:
		}
	})

	azdClient, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)

	t.Cleanup(func() {
		azdClient.Close()
	})

	return azdClient
}
