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
	values       map[string]map[string]string // envName -> key -> value
}

func (s *testEnvironmentServiceServer) GetCurrent(context.Context, *azdext.EmptyRequest) (*azdext.EnvironmentResponse, error) {
	if s.current == nil {
		return nil, status.Error(codes.NotFound, "current environment not found")
	}

	return &azdext.EnvironmentResponse{Environment: s.current}, nil
}

func (s *testEnvironmentServiceServer) Get(
	_ context.Context, req *azdext.GetEnvironmentRequest,
) (*azdext.EnvironmentResponse, error) {
	env, ok := s.environments[req.Name]
	if !ok {
		return nil, status.Error(codes.NotFound, "environment not found")
	}

	return &azdext.EnvironmentResponse{Environment: env}, nil
}

func (s *testEnvironmentServiceServer) SetValue(
	_ context.Context, req *azdext.SetEnvRequest,
) (*azdext.EmptyResponse, error) {
	if s.values == nil {
		s.values = make(map[string]map[string]string)
	}
	if s.values[req.EnvName] == nil {
		s.values[req.EnvName] = make(map[string]string)
	}
	s.values[req.EnvName][req.Key] = req.Value
	return &azdext.EmptyResponse{}, nil
}

func (s *testEnvironmentServiceServer) GetValue(
	_ context.Context, req *azdext.GetEnvRequest,
) (*azdext.KeyValueResponse, error) {
	if s.values != nil {
		if envVals, ok := s.values[req.EnvName]; ok {
			if val, ok := envVals[req.Key]; ok {
				return &azdext.KeyValueResponse{Value: val}, nil
			}
		}
	}
	return nil, status.Error(codes.NotFound, "key not found")
}

func (s *testEnvironmentServiceServer) GetValues(
	_ context.Context, req *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	values := s.values[req.Name]
	keyValues := make([]*azdext.KeyValue, 0, len(values))
	for key, value := range values {
		keyValues = append(keyValues, &azdext.KeyValue{Key: key, Value: value})
	}

	return &azdext.KeyValueListResponse{KeyValues: keyValues}, nil
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
	promptServers ...azdext.PromptServiceServer,
) *azdext.AzdClient {
	t.Helper()

	grpcServer := grpc.NewServer()
	azdext.RegisterEnvironmentServiceServer(grpcServer, envServer)
	azdext.RegisterWorkflowServiceServer(grpcServer, workflowServer)
	if len(promptServers) > 0 {
		azdext.RegisterPromptServiceServer(grpcServer, promptServers[0])
	}

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

func TestNormalizeLoginServer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"myregistry.azurecr.io", "myregistry.azurecr.io"},
		{"https://myregistry.azurecr.io", "myregistry.azurecr.io"},
		{"http://myregistry.azurecr.io", "myregistry.azurecr.io"},
		{"https://myregistry.azurecr.io/", "myregistry.azurecr.io"},
		{"https://crdyt765he4tmsy.azurecr.io", "crdyt765he4tmsy.azurecr.io"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, normalizeLoginServer(tt.input))
		})
	}
}

func TestSetEnvValue_PersistsKeyValue(t *testing.T) {
	t.Parallel()

	envServer := &testEnvironmentServiceServer{
		environments: map[string]*azdext.Environment{
			"test-env": {Name: "test-env"},
		},
	}
	workflowServer := &testWorkflowServiceServer{}
	azdClient := newTestAzdClient(t, envServer, workflowServer)

	const envName = "test-env"

	// Set a value
	err := setEnvValue(
		t.Context(), azdClient, envName, "USE_EXISTING_AI_PROJECT", "true",
	)
	require.NoError(t, err)

	// Verify it was stored
	require.Equal(t, "true", envServer.values[envName]["USE_EXISTING_AI_PROJECT"])

	// Overwrite with "false" (simulating re-init choosing "create new")
	err = setEnvValue(
		t.Context(), azdClient, envName, "USE_EXISTING_AI_PROJECT", "false",
	)
	require.NoError(t, err)

	// Verify the value was updated
	require.Equal(t, "false", envServer.values[envName]["USE_EXISTING_AI_PROJECT"])
}

func TestMissingInitAzureContextValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		azureContext *azdext.AzureContext
		want         []string
	}{
		{
			name: "nil context",
			want: []string{"AZURE_SUBSCRIPTION_ID", "AZURE_LOCATION"},
		},
		{
			name:         "nil scope",
			azureContext: &azdext.AzureContext{},
			want:         []string{"AZURE_SUBSCRIPTION_ID", "AZURE_LOCATION"},
		},
		{
			name: "missing both",
			azureContext: &azdext.AzureContext{
				Scope: &azdext.AzureScope{},
			},
			want: []string{"AZURE_SUBSCRIPTION_ID", "AZURE_LOCATION"},
		},
		{
			name: "missing subscription",
			azureContext: &azdext.AzureContext{
				Scope: &azdext.AzureScope{Location: "eastus"},
			},
			want: []string{"AZURE_SUBSCRIPTION_ID"},
		},
		{
			name: "missing location",
			azureContext: &azdext.AzureContext{
				Scope: &azdext.AzureScope{SubscriptionId: "sub-id"},
			},
			want: []string{"AZURE_LOCATION"},
		},
		{
			name: "complete",
			azureContext: &azdext.AzureContext{
				Scope: &azdext.AzureScope{SubscriptionId: "sub-id", Location: "eastus"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, missingInitAzureContextValues(tt.azureContext))
		})
	}
}

func TestShouldDeferInitAzureContext(t *testing.T) {
	t.Parallel()

	require.False(t, shouldDeferInitAzureContext(false, &azdext.AzureContext{}))
	require.True(t, shouldDeferInitAzureContext(true, &azdext.AzureContext{}))
	require.False(t, shouldDeferInitAzureContext(true, &azdext.AzureContext{
		Scope: &azdext.AzureScope{SubscriptionId: "sub-id", Location: "eastus"},
	}))
}

func TestConfigureDeferredInitAzureContext_PersistsProjectSignalOnly(t *testing.T) {
	const envName = "test-env"

	envServer := &testEnvironmentServiceServer{
		values: map[string]map[string]string{envName: {}},
	}
	azdClient := newTestAzdClient(t, envServer, &testWorkflowServiceServer{})
	azureContext := &azdext.AzureContext{
		Scope: &azdext.AzureScope{SubscriptionId: "sub-id"},
	}

	output, err := captureStdout(t, func() error {
		return configureDeferredInitAzureContext(
			t.Context(), azdClient, envName, azureContext, true,
		)
	})

	require.NoError(t, err)
	require.Equal(t, "false", envServer.values[envName]["USE_EXISTING_AI_PROJECT"])
	require.Equal(t, pendingReasonProject, envServer.values[envName][pendingProvisionEnvVar])
	require.Contains(t, output, "Missing Azure environment values: AZURE_LOCATION")
	require.Contains(t, output, "azd env set AZURE_LOCATION <region>")
	require.NotContains(t, output, "azd env set AZURE_SUBSCRIPTION_ID")
	require.Contains(t, output, "Model resource configuration was deferred")
	require.Contains(t, output, "deployments:")
	require.Contains(t, output, "format: OpenAI")
}

// TestConfigureFoundryProjectEnv_BicepLessShortCircuits verifies that
// bicepless=true seeds identity env vars and returns before any Foundry
// data-plane call. The nil credential turns a regression that re-enables
// connection discovery into a nil-pointer panic.
func TestConfigureFoundryProjectEnv_BicepLessShortCircuits(t *testing.T) {
	t.Parallel()

	const envName = "test-env"
	envServer := &testEnvironmentServiceServer{
		environments: map[string]*azdext.Environment{envName: {Name: envName}},
	}
	azdClient := newTestAzdClient(t, envServer, &testWorkflowServiceServer{})

	project := FoundryProjectInfo{
		SubscriptionId:    "00000000-0000-0000-0000-000000000000",
		ResourceGroupName: "rg-test",
		AccountName:       "acct-test",
		ProjectName:       "proj-test",
		Location:          "eastus2",
	}

	err := configureFoundryProjectEnv(
		t.Context(), azdClient, nil, envName,
		project, project.SubscriptionId,
		false, // skipACR
		true,  // bicepless
	)
	require.NoError(t, err)

	written := envServer.values[envName]

	for _, key := range []string{
		"AZURE_AI_PROJECT_ID",
		"AZURE_RESOURCE_GROUP",
		"AZURE_AI_ACCOUNT_NAME",
		"AZURE_AI_PROJECT_NAME",
		"FOUNDRY_PROJECT_ENDPOINT",
		"AZURE_OPENAI_ENDPOINT",
	} {
		require.NotEmpty(t, written[key], "expected basic project env var %q to be set", key)
	}

	for _, key := range []string{
		"AZURE_CONTAINER_REGISTRY_ENDPOINT",
		"AZURE_CONTAINER_REGISTRY_RESOURCE_ID",
		"AZURE_AI_PROJECT_ACR_CONNECTION_NAME",
		"APPLICATIONINSIGHTS_CONNECTION_STRING",
		"APPLICATIONINSIGHTS_RESOURCE_ID",
		"APPLICATIONINSIGHTS_CONNECTION_NAME",
	} {
		require.Empty(t, written[key], "must not write %q when bicepless is true", key)
	}
}
