// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	azcloud "github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1 "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

type environmentServer struct {
	v1.UnimplementedEnvironmentServiceServer
	values       map[string]string
	name         *string
	currentErr   error
	valuesErr    error
	setErr       error
	writes       []*v1.SetEnvRequest
	reads        []*v1.GetEnvironmentRequest
	currentCalls int
	cloudName    *string
	cloudErr     error
	cloudReads   []*v1.GetConfigStringRequest
	selectedErr  error
}

func (s *environmentServer) Get(
	_ context.Context, request *v1.GetEnvironmentRequest,
) (*v1.EnvironmentResponse, error) {
	return &v1.EnvironmentResponse{Environment: &v1.Environment{Name: request.Name}}, s.selectedErr
}

func (s *environmentServer) GetConfigString(
	_ context.Context, request *v1.GetConfigStringRequest,
) (*v1.GetConfigStringResponse, error) {
	s.cloudReads = append(s.cloudReads, request)
	if request.Path != "cloud.name" {
		return nil, errors.New("unexpected environment configuration path")
	}
	response := &v1.GetConfigStringResponse{}
	if s.cloudName != nil {
		response.Value, response.Found = *s.cloudName, true
	}
	return response, s.cloudErr
}

func (s *environmentServer) GetCurrent(context.Context, *v1.EmptyRequest) (*v1.EnvironmentResponse, error) {
	s.currentCalls++
	if s.currentErr != nil {
		return nil, s.currentErr
	}
	name := "dev"
	if s.name != nil {
		name = *s.name
	}
	return &v1.EnvironmentResponse{Environment: &v1.Environment{Name: name}}, nil
}

func (s *environmentServer) GetValues(
	_ context.Context, request *v1.GetEnvironmentRequest,
) (*v1.KeyValueListResponse, error) {
	s.reads = append(s.reads, request)
	if s.valuesErr != nil {
		return nil, s.valuesErr
	}
	response := &v1.KeyValueListResponse{}
	for key, value := range s.values {
		response.KeyValues = append(response.KeyValues, &v1.KeyValue{Key: key, Value: value})
	}
	return response, nil
}

func (s *environmentServer) SetValue(_ context.Context, request *v1.SetEnvRequest) (*v1.EmptyResponse, error) {
	if s.setErr != nil {
		return nil, s.setErr
	}
	s.writes = append(s.writes, request)
	s.values[request.Key] = request.Value
	return &v1.EmptyResponse{}, nil
}

type accountServer struct {
	v1.UnimplementedAccountServiceServer
	tenant       *string
	tenantErr    error
	lookups      []*v1.LookupTenantRequest
	principal    *v1.GetCurrentPrincipalResponse
	principalErr error
	principals   []*v1.GetCurrentPrincipalRequest
}

type projectServer struct {
	v1.UnimplementedProjectServiceServer
	name             *string
	err              error
	cloudName        string
	cloudErr         error
	projectCloudName *string
}

type userConfigServer struct {
	v1.UnimplementedUserConfigServiceServer
	project *projectServer
}

func (s *userConfigServer) GetString(
	ctx context.Context, request *v1.GetUserConfigStringRequest,
) (*v1.GetUserConfigStringResponse, error) {
	return s.project.GetString(ctx, request)
}

func (s *projectServer) GetConfigValue(
	_ context.Context, request *v1.GetProjectConfigValueRequest,
) (*v1.GetProjectConfigValueResponse, error) {
	if request.Path != "cloud.name" {
		return nil, errors.New("unexpected project configuration path")
	}
	if s.projectCloudName == nil {
		return &v1.GetProjectConfigValueResponse{}, nil
	}
	return &v1.GetProjectConfigValueResponse{Found: true, Value: structpb.NewStringValue(*s.projectCloudName)}, nil
}

func (s *projectServer) GetString(
	_ context.Context, request *v1.GetUserConfigStringRequest,
) (*v1.GetUserConfigStringResponse, error) {
	if request.Path != "cloud.name" {
		return nil, errors.New("unexpected configuration path")
	}
	return &v1.GetUserConfigStringResponse{Value: s.cloudName}, s.cloudErr
}

func (s *projectServer) Get(context.Context, *v1.EmptyRequest) (*v1.GetProjectResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	name := "example"
	if s.name != nil {
		name = *s.name
	}
	return &v1.GetProjectResponse{Project: &v1.ProjectConfig{Name: name}}, nil
}

func (s *accountServer) LookupTenant(
	_ context.Context, request *v1.LookupTenantRequest,
) (*v1.LookupTenantResponse, error) {
	s.lookups = append(s.lookups, request)
	if s.tenantErr != nil {
		return nil, s.tenantErr
	}
	tenant := "user-access-tenant"
	if s.tenant != nil {
		tenant = *s.tenant
	}
	return &v1.LookupTenantResponse{TenantId: tenant}, nil
}

func (s *accountServer) GetCurrentPrincipal(
	_ context.Context, request *v1.GetCurrentPrincipalRequest,
) (*v1.GetCurrentPrincipalResponse, error) {
	s.principals = append(s.principals, request)
	if s.principalErr != nil {
		return nil, s.principalErr
	}
	if request.SubscriptionId != "sub" {
		return nil, errors.New("principal must be resolved in the selected subscription")
	}
	if s.principal != nil {
		return s.principal, nil
	}
	return &v1.GetCurrentPrincipalResponse{
		ObjectId: "resource-tenant-object-id", PrincipalType: v1.PrincipalType_PRINCIPAL_TYPE_SERVICE_PRINCIPAL,
	}, nil
}

type promptServer struct {
	v1.UnimplementedPromptServiceServer
	aiRequests           []*v1.PromptAiLocationWithQuotaRequest
	aiLocation           string
	aiErr                error
	promptRequests       []*v1.PromptRequest
	promptValue          string
	promptErr            error
	confirms             []*v1.ConfirmRequest
	confirmed            bool
	confirmErr           error
	subscription         *v1.Subscription
	subscriptionErr      error
	subscriptionRequests int
	location             *v1.Location
	locationErr          error
	locations            []*v1.PromptLocationRequest
}

func (s *promptServer) Prompt(_ context.Context, request *v1.PromptRequest) (*v1.PromptResponse, error) {
	s.promptRequests = append(s.promptRequests, request)
	if s.promptErr != nil {
		return nil, s.promptErr
	}
	return &v1.PromptResponse{Value: s.promptValue}, nil
}

func (s *promptServer) Confirm(_ context.Context, request *v1.ConfirmRequest) (*v1.ConfirmResponse, error) {
	s.confirms = append(s.confirms, request)
	if s.confirmErr != nil {
		return nil, s.confirmErr
	}
	return &v1.ConfirmResponse{Value: new(s.confirmed)}, nil
}

func (s *promptServer) PromptSubscription(
	context.Context, *v1.PromptSubscriptionRequest,
) (*v1.PromptSubscriptionResponse, error) {
	s.subscriptionRequests++
	if s.subscriptionErr != nil {
		return nil, s.subscriptionErr
	}
	if s.subscription != nil {
		return &v1.PromptSubscriptionResponse{Subscription: s.subscription}, nil
	}
	return &v1.PromptSubscriptionResponse{Subscription: &v1.Subscription{
		Id: "sub", TenantId: "different-resource-tenant", UserTenantId: "user-access-tenant",
	}}, nil
}

func (s *promptServer) PromptLocation(
	_ context.Context, request *v1.PromptLocationRequest,
) (*v1.PromptLocationResponse, error) {
	s.locations = append(s.locations, request)
	if s.locationErr != nil {
		return nil, s.locationErr
	}
	if request.AzureContext.Scope.TenantId != "user-access-tenant" {
		return nil, errors.New("location prompt must use the user access tenant")
	}
	if s.location != nil {
		return &v1.PromptLocationResponse{Location: s.location}, nil
	}
	return &v1.PromptLocationResponse{Location: &v1.Location{Name: "eastus"}}, nil
}

func (s *promptServer) PromptAiLocationWithQuota(
	_ context.Context, request *v1.PromptAiLocationWithQuotaRequest,
) (*v1.PromptAiLocationWithQuotaResponse, error) {
	s.aiRequests = append(s.aiRequests, request)
	if s.aiErr != nil {
		return nil, s.aiErr
	}
	return &v1.PromptAiLocationWithQuotaResponse{Location: &v1.Location{Name: s.aiLocation}}, nil
}

func newHostClient(t *testing.T, environment *environmentServer, prompts *promptServer) *azdext.AzdClient {
	t.Helper()
	return newHostClientWithIdentity(t, environment, prompts, &accountServer{}, &projectServer{})
}

func newHostClientWithIdentity(
	t *testing.T, environment *environmentServer, prompts *promptServer, account *accountServer, project *projectServer,
) *azdext.AzdClient {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	v1.RegisterEnvironmentServiceServer(server, environment)
	v1.RegisterAccountServiceServer(server, account)
	v1.RegisterPromptServiceServer(server, prompts)
	v1.RegisterProjectServiceServer(server, project)
	v1.RegisterUserConfigServiceServer(server, &userConfigServer{project: project})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}

func TestDestroyConfirmation(t *testing.T) {
	for _, test := range []struct {
		name      string
		confirmed bool
		force     bool
		promptErr error
		wantErr   bool
	}{
		{name: "accepted", confirmed: true},
		{name: "declined", wantErr: true},
		{name: "prompt failure", promptErr: errors.New("prompt failed"), wantErr: true},
		{name: "forced", force: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			prompts := &promptServer{confirmed: test.confirmed, confirmErr: test.promptErr}
			azure := &fakeBackend{deployment: deployedFixture()}
			p := readyProvider(t, azure)
			p.client = newHostClient(t, &environmentServer{}, prompts)

			_, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: test.force}, nil)
			if (err != nil) != test.wantErr {
				t.Fatalf("destroy error = %v, want error %v", err, test.wantErr)
			}
			if test.force {
				if len(prompts.confirms) != 0 {
					t.Fatal("forced deletion prompted for confirmation")
				}
			} else {
				if len(prompts.confirms) != 1 {
					t.Fatalf("expected one confirmation, got %d", len(prompts.confirms))
				}
				options := prompts.confirms[0].GetOptions()
				want := "Delete resource groups generated and ALL resources inside them, " +
					"including resources not deployed by this layer?"
				if options.GetMessage() != want || options.GetDefaultValue() {
					t.Fatalf("confirmation must disclose group-wide deletion and default to No: %v", options)
				}
			}
			if test.wantErr {
				if len(azure.deletedGroups) != 0 || azure.deletedRecord {
					t.Fatal("failed confirmation deleted infrastructure or its deployment record")
				}
			} else if len(azure.deletedGroups) != 1 || !azure.deletedRecord {
				t.Fatal("confirmed deletion did not remove the group and deployment record")
			}
		})
	}
}

func TestEnsureEnvUsesHostScopeAndPrincipal(t *testing.T) {
	env := &environmentServer{values: map[string]string{}}
	prompts := &promptServer{}
	account := &accountServer{}
	client := newHostClientWithIdentity(t, env, prompts, account, &projectServer{})
	p := New(client, nil)
	if err := p.Initialize(t.Context(), ".", &azdext.ProvisioningOptions{Path: "testdata", Name: "app"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AZURE_SUBSCRIPTION_ID", "")
	t.Setenv("AZURE_LOCATION", "")
	if err := p.EnsureEnv(t.Context()); err != nil {
		t.Fatal(err)
	}
	if p.scope.principalID != "resource-tenant-object-id" ||
		p.scope.principalType != "ServicePrincipal" {
		t.Fatalf("principal resolution lost tenant identity: %v", p.scope.principalType)
	}
	if p.scope.subscriptionID != "sub" || p.scope.location != "eastus" {
		t.Fatal("host prompts did not configure the Azure scope")
	}
	if p.name != deploymentName("example", "dev", "app") {
		t.Fatal("layer identity was not included in the deployment name")
	}
	require.Equal(t, "dev", p.envName)
	require.Equal(t, "user-access-tenant", p.scope.tenantID)
	require.Equal(t, map[string]string{"AZURE_SUBSCRIPTION_ID": "sub", "AZURE_LOCATION": "eastus"}, env.values)
	require.Len(t, env.writes, 2)
	for _, write := range env.writes {
		require.Equal(t, "dev", write.EnvName)
	}
	require.Equal(t, 1, prompts.subscriptionRequests)
	require.Empty(t, account.lookups, "selected user access tenant should not be looked up again")
	require.Len(t, prompts.locations, 1)
	require.Equal(t, "sub", prompts.locations[0].AzureContext.Scope.SubscriptionId)
	require.NotNil(t, p.azure)

	p.client = nil
	require.NoError(t, p.EnsureEnv(t.Context()), "completed setup should not contact the host again")
}

func TestEnsureEnvRequiresInitialization(t *testing.T) {
	require.ErrorContains(t, New(nil, nil).EnsureEnv(t.Context()), "provider is not initialized")
}

func TestEnsureEnvValuePrecedenceAndSavedScope(t *testing.T) {
	t.Setenv("AZURE_SUBSCRIPTION_ID", "process-sub")
	t.Setenv("AZURE_LOCATION", "process-location")
	t.Setenv("AZURE_ENV_NAME", "process-env")
	t.Setenv("PROCESS_ONLY", "process")
	t.Setenv("SHARED", "process")
	env := &environmentServer{values: map[string]string{
		"AZURE_SUBSCRIPTION_ID": "saved-sub", "AZURE_LOCATION": "saved-location",
		"AZURE_ENV_NAME": "saved-env", "SHARED": "saved", "SAVED_ONLY": "saved",
	}}
	prompts := &promptServer{}
	account := &accountServer{principal: &v1.GetCurrentPrincipalResponse{
		ObjectId: "user-id", PrincipalType: v1.PrincipalType_PRINCIPAL_TYPE_USER,
	}}
	p := New(newHostClientWithIdentity(t, env, prompts, account, &projectServer{}), nil)
	require.NoError(t, p.Initialize(t.Context(), ".", &azdext.ProvisioningOptions{
		Path: "testdata", Name: "app",
		VirtualEnv: map[string]string{
			"AZURE_SUBSCRIPTION_ID": "sub", "AZURE_LOCATION": "virtual-location",
			"AZURE_ENV_NAME": "virtual-env", "VIRTUAL_ONLY": "virtual",
		},
	}))
	backend := &fakeBackend{}
	p.azure = backend

	require.NoError(t, p.EnsureEnv(t.Context()))
	require.Equal(t, &scope{
		subscriptionID: "sub", tenantID: "user-access-tenant", location: "virtual-location",
		principalID: "user-id", principalType: "User",
	}, p.scope)
	values, err := p.loadEnvironmentValues(t.Context())
	require.NoError(t, err)
	for key, value := range map[string]string{
		"AZURE_SUBSCRIPTION_ID": "sub", "AZURE_LOCATION": "virtual-location", "AZURE_ENV_NAME": "dev",
		"PROCESS_ONLY": "process", "SAVED_ONLY": "saved", "VIRTUAL_ONLY": "virtual", "SHARED": "saved",
	} {
		require.Equal(t, value, values[key], key)
	}
	require.Zero(t, prompts.subscriptionRequests)
	require.Empty(t, prompts.locations)
	require.Empty(t, env.writes)
	require.Len(t, account.lookups, 1)
	require.Equal(t, "sub", account.lookups[0].SubscriptionId)
	require.Same(t, backend, p.azure)
}

func TestEnsureEnvIdentityFailures(t *testing.T) {
	for _, test := range []struct {
		name    string
		env     environmentServer
		project projectServer
		want    string
	}{
		{name: "active environment RPC", env: environmentServer{currentErr: errors.New("host failed")},
			want: "getting active environment"},
		{name: "no active environment", env: environmentServer{name: new("")}, want: "active azd environment is required"},
		{name: "project RPC", project: projectServer{err: errors.New("host failed")}, want: "getting project identity"},
		{name: "unnamed project", project: projectServer{name: new("")}, want: "project name is required"},
		{name: "environment values RPC", env: environmentServer{valuesErr: errors.New("host failed")},
			want: "reading environment values"},
	} {
		t.Run(test.name, func(t *testing.T) {
			prompts := &promptServer{}
			account := &accountServer{}
			p := New(newHostClientWithIdentity(t, &test.env, prompts, account, &test.project), nil)
			require.NoError(t, p.Initialize(t.Context(), ".", &azdext.ProvisioningOptions{Path: "testdata"}))
			require.ErrorContains(t, p.EnsureEnv(t.Context()), test.want)
			require.Nil(t, p.scope)
			require.Nil(t, p.azure)
			require.Empty(t, p.name)
			require.Empty(t, test.env.writes)
			require.Zero(t, prompts.subscriptionRequests)
			require.Empty(t, account.principals)
		})
	}
}

func TestEnsureEnvSubscriptionFailures(t *testing.T) {
	for _, test := range []struct {
		name         string
		subscription string
		prompts      promptServer
		account      accountServer
		setErr       error
		want         string
	}{
		{name: "subscription prompt", prompts: promptServer{subscriptionErr: errors.New("selection failed")},
			want: "selecting subscription"},
		{name: "empty subscription", prompts: promptServer{subscription: &v1.Subscription{UserTenantId: "tenant"}},
			want: "no subscription or user access tenant"},
		{name: "missing access tenant", prompts: promptServer{subscription: &v1.Subscription{Id: "sub", TenantId: "tenant"}},
			want: "no subscription or user access tenant"},
		{name: "subscription persistence", setErr: errors.New("save failed"),
			want: "saving environment value AZURE_SUBSCRIPTION_ID"},
		{name: "tenant lookup", subscription: "sub", account: accountServer{tenantErr: errors.New("lookup failed")},
			want: "resolving subscription access tenant"},
		{name: "empty tenant", subscription: "sub", account: accountServer{tenant: new("")},
			want: "subscription access tenant is empty"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("AZURE_SUBSCRIPTION_ID", "")
			t.Setenv("AZURE_LOCATION", "")
			env := &environmentServer{
				values: map[string]string{"AZURE_SUBSCRIPTION_ID": test.subscription}, setErr: test.setErr,
			}
			p := New(newHostClientWithIdentity(t, env, &test.prompts, &test.account, &projectServer{}), nil)
			require.NoError(t, p.Initialize(t.Context(), ".", &azdext.ProvisioningOptions{Path: "testdata"}))
			require.ErrorContains(t, p.EnsureEnv(t.Context()), test.want)
			require.Nil(t, p.scope)
			require.Nil(t, p.azure)
			require.Empty(t, p.name)
			require.Empty(t, env.writes)
			require.Empty(t, test.prompts.locations)
			require.Empty(t, test.account.principals)
		})
	}
}

func TestEnsureEnvLocationFailures(t *testing.T) {
	for _, test := range []struct {
		name    string
		prompts promptServer
		setErr  error
		want    string
	}{
		{name: "location prompt", prompts: promptServer{locationErr: errors.New("selection failed")},
			want: "selecting location"},
		{name: "empty location", prompts: promptServer{location: &v1.Location{}}, want: "empty location"},
		{name: "location persistence", setErr: errors.New("save failed"), want: "saving environment value AZURE_LOCATION"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("AZURE_LOCATION", "")
			env := &environmentServer{values: map[string]string{"AZURE_SUBSCRIPTION_ID": "sub"}, setErr: test.setErr}
			account := &accountServer{}
			p := New(newHostClientWithIdentity(t, env, &test.prompts, account, &projectServer{}), nil)
			require.NoError(t, p.Initialize(t.Context(), ".", &azdext.ProvisioningOptions{Path: "testdata"}))
			require.ErrorContains(t, p.EnsureEnv(t.Context()), test.want)
			require.Nil(t, p.scope)
			require.Nil(t, p.azure)
			require.Empty(t, env.writes)
			require.Empty(t, account.principals)
			require.Len(t, test.prompts.locations, 1)
			require.Equal(t, "user-access-tenant", test.prompts.locations[0].AzureContext.Scope.TenantId)
		})
	}
}

func TestEnsureEnvPrincipalAndCredentialFailures(t *testing.T) {
	for _, test := range []struct {
		name    string
		account accountServer
		want    string
	}{
		{name: "principal RPC", account: accountServer{principalErr: errors.New("lookup failed")},
			want: "resolving current principal"},
		{name: "unknown principal type", account: accountServer{principal: &v1.GetCurrentPrincipalResponse{ObjectId: "id"}},
			want: "current principal type is unknown"},
		{name: "empty object ID", account: accountServer{principal: &v1.GetCurrentPrincipalResponse{
			PrincipalType: v1.PrincipalType_PRINCIPAL_TYPE_USER,
		}}, want: "current principal object ID is empty"},
		{name: "invalid credential tenant", account: accountServer{tenant: new("invalid tenant!")},
			want: "creating Azure credential"},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := &environmentServer{values: map[string]string{"AZURE_SUBSCRIPTION_ID": "sub", "AZURE_LOCATION": "eastus"}}
			p := New(newHostClientWithIdentity(t, env, &promptServer{}, &test.account, &projectServer{}), nil)
			require.NoError(t, p.Initialize(t.Context(), ".", &azdext.ProvisioningOptions{Path: "testdata"}))
			require.ErrorContains(t, p.EnsureEnv(t.Context()), test.want)
			require.Nil(t, p.scope)
			require.Nil(t, p.azure)
			require.Empty(t, p.name)
			require.Empty(t, env.writes)
		})
	}
}

func TestEnsureEnvRetriesAfterPersistenceFailure(t *testing.T) {
	t.Setenv("AZURE_LOCATION", "")
	env := &environmentServer{
		values: map[string]string{"AZURE_SUBSCRIPTION_ID": "sub"}, setErr: errors.New("save failed"),
	}
	prompts := &promptServer{}
	p := New(newHostClient(t, env, prompts), nil)
	require.NoError(t, p.Initialize(t.Context(), ".", &azdext.ProvisioningOptions{Path: "testdata"}))
	require.ErrorContains(t, p.EnsureEnv(t.Context()), "save failed")
	require.Nil(t, p.scope)
	require.NotContains(t, env.values, "AZURE_LOCATION")

	env.setErr = nil
	require.NoError(t, p.EnsureEnv(t.Context()))
	require.Equal(t, "eastus", p.scope.location)
	require.Equal(t, "eastus", env.values["AZURE_LOCATION"])
	require.Len(t, prompts.locations, 2)
}

func TestEnsureEnvRejectsMissingSelectedEnvironment(t *testing.T) {
	t.Setenv("AZD_ENVIRONMENT", "missing")
	env := &environmentServer{selectedErr: status.Error(codes.NotFound, "environment not found")}
	p := New(newHostClient(t, env, &promptServer{}), nil)
	require.NoError(t, p.Initialize(t.Context(), ".", &azdext.ProvisioningOptions{Path: "testdata", Name: "data"}))
	require.ErrorContains(t, p.EnsureEnv(t.Context()), `getting environment "missing"`)
	require.Zero(t, env.currentCalls)
	require.Empty(t, env.reads)
	require.Empty(t, env.writes)
	require.Nil(t, p.scope)
	require.Nil(t, p.azure)
}

func TestEnsureEnvUsesSelectedEnvironment(t *testing.T) {
	t.Setenv("AZD_ENVIRONMENT", "prod")
	env := &environmentServer{
		values:     map[string]string{"AZURE_SUBSCRIPTION_ID": "sub"},
		currentErr: errors.New("default environment must not be read"),
	}
	p := New(newHostClient(t, env, &promptServer{}), nil)
	require.NoError(t, p.Initialize(t.Context(), ".", &azdext.ProvisioningOptions{Path: "testdata", Name: "data"}))
	require.NoError(t, p.EnsureEnv(t.Context()))
	require.Zero(t, env.currentCalls)
	require.Len(t, env.reads, 1)
	require.Equal(t, "prod", env.reads[0].Name)
	require.Equal(t, "prod", env.cloudReads[0].EnvName)
	require.Equal(t, "prod", p.envName)
	require.Equal(t, deploymentName("example", "prod", "data"), p.name)
	for _, write := range env.writes {
		require.Equal(t, "prod", write.EnvName)
	}
}

func TestLoadCloudUsesConfigurationPrecedence(t *testing.T) {
	for _, test := range []struct {
		name        string
		environment *string
		project     *string
		user        string
		want        string
	}{
		{name: "default", want: "https://management.azure.com"},
		{name: "user", user: "AzureUSGovernment", want: "https://management.usgovcloudapi.net"},
		{name: "project", project: new("AzureChinaCloud"), user: "AzureUSGovernment",
			want: "https://management.chinacloudapi.cn"},
		{name: "environment", environment: new("AzureCloud"), project: new("AzureChinaCloud"),
			user: "AzureUSGovernment", want: "https://management.azure.com"},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := &environmentServer{cloudName: test.environment}
			project := &projectServer{projectCloudName: test.project, cloudName: test.user}
			p := New(newHostClientWithIdentity(t, env, &promptServer{}, &accountServer{}, project), nil)
			p.envName = "staging"
			configured, err := p.loadCloud(t.Context())
			require.NoError(t, err)
			require.Equal(t, test.want, strings.TrimSuffix(
				configured.Configuration.Services[azcloud.ResourceManager].Endpoint, "/"))
			require.Equal(t, "staging", env.cloudReads[0].EnvName)
		})
	}
}

func TestEnsureEnvRejectsInvalidCloudBeforeAzureCalls(t *testing.T) {
	for _, test := range []struct {
		name  string
		cloud string
		err   error
		want  string
	}{
		{name: "unknown cloud", cloud: "unknown", want: "resolving Azure cloud configuration"},
		{name: "configuration failure", err: errors.New("config unavailable"), want: "config unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := &environmentServer{values: map[string]string{"AZURE_SUBSCRIPTION_ID": "sub", "AZURE_LOCATION": "eastus"}}
			account := &accountServer{}
			p := New(newHostClientWithIdentity(t, env, &promptServer{}, account,
				&projectServer{cloudName: test.cloud, cloudErr: test.err}), nil)
			require.NoError(t, p.Initialize(t.Context(), ".", &azdext.ProvisioningOptions{Path: "testdata"}))
			require.ErrorContains(t, p.EnsureEnv(t.Context()), test.want)
			require.Empty(t, account.lookups)
			require.Empty(t, account.principals)
			require.Empty(t, env.writes)
		})
	}
}
