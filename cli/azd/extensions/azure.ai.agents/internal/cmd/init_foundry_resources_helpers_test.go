// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"net"
	"testing"

	armcognitiveservices "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
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

func TestFoundryProjectInfoFromResource(t *testing.T) {
	t.Parallel()

	resourceId := "/subscriptions/sub-id/resourceGroups/my-rg/providers/Microsoft.CognitiveServices/accounts/my-account/projects/my-project"

	tests := []struct {
		name     string
		resource *armresources.GenericResourceExpanded
		want     *FoundryProjectInfo
	}{
		{
			name: "maps filtered subscription resource",
			resource: &armresources.GenericResourceExpanded{
				ID:       new(resourceId),
				Location: new("eastus"),
			},
			want: &FoundryProjectInfo{
				SubscriptionId:    "sub-id",
				ResourceGroupName: "my-rg",
				AccountName:       "my-account",
				ProjectName:       "my-project",
				Location:          "eastus",
				ResourceId:        resourceId,
			},
		},
		{
			name:     "skips resource without id",
			resource: &armresources.GenericResourceExpanded{},
		},
		{
			name: "skips malformed project resource id",
			resource: &armresources.GenericResourceExpanded{
				ID: new("/subscriptions/sub-id/resourceGroups/my-rg/providers/Microsoft.CognitiveServices/accounts/my-account"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := foundryProjectInfoFromResource(tt.resource)
			if tt.want == nil {
				require.False(t, ok)
				require.Nil(t, got)
				return
			}

			require.True(t, ok)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestAgentModelFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		locations         []string
		excludeModelNames []string
		wantLocations     []string
		wantExclude       []string
	}{
		{
			name:              "AllPopulated",
			locations:         []string{"eastus2"},
			excludeModelNames: []string{"gpt-4.1-mini"},
			wantLocations:     []string{"eastus2"},
			wantExclude:       []string{"gpt-4.1-mini"},
		},
		{
			name:              "BothNil",
			locations:         nil,
			excludeModelNames: nil,
			wantLocations:     nil,
			wantExclude:       nil,
		},
		{
			name:              "EmptySlices",
			locations:         []string{},
			excludeModelNames: []string{},
			wantLocations:     nil,
			wantExclude:       nil,
		},
		{
			name:              "OnlyLocations",
			locations:         []string{"westus", "eastus"},
			excludeModelNames: nil,
			wantLocations:     []string{"westus", "eastus"},
			wantExclude:       nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			filter := agentModelFilter(tt.locations, tt.excludeModelNames)

			require.Equal(t, []string{agentsV2ModelCapability}, filter.Capabilities)
			require.Equal(t, tt.wantLocations, filter.Locations)
			require.Equal(t, tt.wantExclude, filter.ExcludeModelNames)
		})
	}
}

func TestLocationAllowed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		location         string
		allowedLocations []string
		want             bool
	}{
		{
			name:             "EmptyAllowedMeansAllowAll",
			location:         "anyregion",
			allowedLocations: nil,
			want:             true,
		},
		{
			name:             "EmptySliceAllowsAll",
			location:         "anyregion",
			allowedLocations: []string{},
			want:             true,
		},
		{
			name:             "ExactMatch",
			location:         "eastus",
			allowedLocations: []string{"eastus", "westus"},
			want:             true,
		},
		{
			name:             "CaseInsensitiveMatch",
			location:         "EastUS",
			allowedLocations: []string{"eastus", "westus"},
			want:             true,
		},
		{
			name:             "WhitespaceHandled",
			location:         "  eastus  ",
			allowedLocations: []string{"eastus"},
			want:             true,
		},
		{
			name:             "NotInList",
			location:         "northeurope",
			allowedLocations: []string{"eastus", "westus"},
			want:             false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, locationAllowed(tt.location, tt.allowedLocations))
		})
	}
}

func TestNormalizeLocationName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"EastUS", "eastus"},
		{"  westus  ", "westus"},
		{"NORTHEUROPE", "northeurope"},
		{"eastus2", "eastus2"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, normalizeLocationName(tt.input))
		})
	}
}

func TestUpdateFoundryProjectInfo(t *testing.T) {
	t.Parallel()

	project := &FoundryProjectInfo{
		SubscriptionId:    "sub-id",
		ResourceGroupName: "my-rg",
		AccountName:       "my-account",
		ProjectName:       "my-project",
		ResourceId:        "/subscriptions/sub-id/resourceGroups/my-rg/providers/Microsoft.CognitiveServices/accounts/my-account/projects/my-project",
	}

	updateFoundryProjectInfo(project, &armcognitiveservices.Project{
		ID:       new("/subscriptions/sub-id/resourceGroups/my-rg/providers/Microsoft.CognitiveServices/accounts/my-account/projects/my-project"),
		Name:     new("my-account/updated-project"),
		Location: new("westus"),
	})

	require.Equal(t, "updated-project", project.ProjectName)
	require.Equal(t, "westus", project.Location)
	require.Equal(
		t,
		"/subscriptions/sub-id/resourceGroups/my-rg/providers/Microsoft.CognitiveServices/accounts/my-account/projects/my-project",
		project.ResourceId,
	)
}

func TestGetFoundryProject_SubscriptionMismatch(t *testing.T) {
	t.Parallel()

	_, err := getFoundryProject(
		t.Context(),
		nil,
		"selected-subscription",
		"/subscriptions/other-subscription/resourceGroups/my-rg/providers/Microsoft.CognitiveServices/accounts/my-account/projects/my-project",
	)

	require.Error(t, err)
	require.Contains(t, err.Error(), "does not match the selected subscription")
}
