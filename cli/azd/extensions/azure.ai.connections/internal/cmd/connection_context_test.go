// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"

	"azure.ai.connections/internal/exterrors"
	"azure.ai.connections/internal/foundry/projectctx"
	"azure.ai.connections/internal/pkg/envkey"
)

// TestNewCredential covers both credential shapes: the default (home) tenant
// when no tenant is resolved, and a tenant-scoped credential for multi-tenant /
// guest users. The tenant-scoped branch is what fixes "Tenant provided in token
// does not match resource token".
func TestNewCredential(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		tenantID string
	}{
		{name: "default tenant", tenantID: ""},
		{name: "scoped tenant", tenantID: "11111111-1111-1111-1111-111111111111"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cred, err := newCredential(tc.tenantID)
			require.NoError(t, err)
			require.NotNil(t, cred)
		})
	}
}

func TestResolveEnvContextUsesSelectedEnvironment(t *testing.T) {
	t.Parallel()

	environment := &recordingEnvironmentContextReader{values: map[string]string{
		"AZURE_AI_PROJECT_ID":   "/subscriptions/sub/resourceGroups/rg/providers/provider/accounts/account/projects/project",
		"AZURE_SUBSCRIPTION_ID": "sub",
	}}
	account := &recordingTenantLookup{}

	resolved := resolveEnvContextWithClients(t.Context(), "staging", environment, account)

	assert.Equal(t, environment.values["AZURE_AI_PROJECT_ID"], resolved.projectID)
	assert.Equal(t, "tenant", resolved.tenantID)
	assert.Equal(t, 0, environment.currentCalls)
	assert.Equal(t, []string{"staging"}, environment.requestedEnvironments)
	assert.Zero(t, environment.getValueCalls)
	assert.Equal(t, 1, environment.getValuesCalls)
	assert.Equal(t, []string{"sub"}, account.subscriptions)
}

func TestResolveEnvContextDoesNotUseProcessFallback(t *testing.T) {
	t.Setenv("AZURE_AI_PROJECT_ID", "process-project")
	t.Setenv("AZURE_SUBSCRIPTION_ID", "process-subscription")
	for _, tt := range []struct {
		name          string
		values        map[string]string
		readErr       error
		wantProjectID string
		wantTenant    string
	}{
		{name: "neither value persisted"},
		{name: "project only", values: map[string]string{"AZURE_AI_PROJECT_ID": "persisted-project"},
			wantProjectID: "persisted-project"},
		{name: "subscription only", values: map[string]string{"AZURE_SUBSCRIPTION_ID": "persisted-sub"},
			wantTenant: "tenant"},
		{name: "empty persisted values", values: map[string]string{"AZURE_AI_PROJECT_ID": "", "AZURE_SUBSCRIPTION_ID": ""}},
		{name: "read fails", readErr: errors.New("unavailable")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			environment := &recordingEnvironmentContextReader{values: tt.values, valuesErr: tt.readErr}
			account := &recordingTenantLookup{}
			resolved := resolveEnvContextWithClients(t.Context(), "staging", environment, account)
			assert.Equal(t, tt.wantProjectID, resolved.projectID)
			assert.Equal(t, tt.wantTenant, resolved.tenantID)
			assert.Zero(t, environment.currentCalls)
			assert.Zero(t, environment.getValueCalls)
			assert.Equal(t, 1, environment.getValuesCalls)
			assert.Equal(t, []string{"staging"}, environment.requestedEnvironments)
			if tt.wantTenant == "" {
				assert.Empty(t, account.subscriptions)
			} else {
				assert.Equal(t, []string{"persisted-sub"}, account.subscriptions)
			}
		})
	}
}

func TestResolveEnvContextUsesCurrentEnvironmentWhenUnspecified(t *testing.T) {
	t.Parallel()
	environment := &recordingEnvironmentContextReader{values: map[string]string{
		"AZURE_AI_PROJECT_ID": "current-project", "AZURE_SUBSCRIPTION_ID": "current-sub",
	}}
	account := &recordingTenantLookup{}
	resolved := resolveEnvContextWithClients(t.Context(), "", environment, account)
	assert.Equal(t, "current-project", resolved.projectID)
	assert.Equal(t, "tenant", resolved.tenantID)
	assert.Equal(t, 1, environment.currentCalls)
	assert.Equal(t, 2, environment.getValueCalls)
	assert.Zero(t, environment.getValuesCalls)
	assert.Equal(t, []string{"default", "default"}, environment.requestedEnvironments)
	assert.Equal(t, []string{"current-sub"}, account.subscriptions)
}

func TestResolveEnvContextStandaloneProcessFallback(t *testing.T) {
	t.Setenv("AZURE_AI_PROJECT_ID", "process-project")
	t.Setenv("AZURE_SUBSCRIPTION_ID", "process-sub")
	for _, tt := range []struct {
		name          string
		values        map[string]string
		wantProjectID string
		wantSubID     string
	}{
		{name: "neither value persisted", wantProjectID: "process-project", wantSubID: "process-sub"},
		{name: "project persisted", values: map[string]string{"AZURE_AI_PROJECT_ID": "persisted-project"},
			wantProjectID: "persisted-project", wantSubID: "process-sub"},
		{name: "subscription persisted", values: map[string]string{"AZURE_SUBSCRIPTION_ID": "persisted-sub"},
			wantProjectID: "process-project", wantSubID: "persisted-sub"},
		{name: "persisted values win", values: map[string]string{
			"AZURE_AI_PROJECT_ID": "persisted-project", "AZURE_SUBSCRIPTION_ID": "persisted-sub",
		}, wantProjectID: "persisted-project", wantSubID: "persisted-sub"},
		{name: "empty persisted values win", values: map[string]string{
			"AZURE_AI_PROJECT_ID": "", "AZURE_SUBSCRIPTION_ID": "",
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			environment := &recordingEnvironmentContextReader{values: tt.values}
			account := &recordingTenantLookup{}

			resolved := resolveEnvContextWithClients(t.Context(), "", environment, account)

			assert.Equal(t, tt.wantProjectID, resolved.projectID)
			assert.Equal(t, 1, environment.currentCalls)
			assert.Equal(t, 2, environment.getValueCalls)
			assert.Zero(t, environment.getValuesCalls)
			assert.Equal(t, []string{"default", "default"}, environment.requestedEnvironments)
			if tt.wantSubID == "" {
				assert.Empty(t, resolved.tenantID)
				assert.Empty(t, account.subscriptions)
			} else {
				assert.Equal(t, "tenant", resolved.tenantID)
				assert.Equal(t, []string{tt.wantSubID}, account.subscriptions)
			}
		})
	}
}

func TestResolveEnvContextStandaloneOptionalValues(t *testing.T) {
	t.Setenv("AZURE_AI_PROJECT_ID", "")
	t.Setenv("AZURE_SUBSCRIPTION_ID", "")
	for _, tt := range []struct {
		name          string
		values        map[string]string
		valueErrors   map[string]error
		readErr       error
		wantProjectID string
		wantSubID     string
	}{
		{name: "both missing"},
		{name: "project only", values: map[string]string{"AZURE_AI_PROJECT_ID": "current-project"},
			wantProjectID: "current-project"},
		{name: "subscription only", values: map[string]string{"AZURE_SUBSCRIPTION_ID": "current-sub"},
			wantSubID: "current-sub"},
		{name: "project read fails", values: map[string]string{
			"AZURE_AI_PROJECT_ID": "ignored-project", "AZURE_SUBSCRIPTION_ID": "current-sub",
		}, valueErrors: map[string]error{"AZURE_AI_PROJECT_ID": errors.New("unavailable")},
			wantSubID: "current-sub"},
		{name: "subscription read fails", values: map[string]string{
			"AZURE_AI_PROJECT_ID": "current-project", "AZURE_SUBSCRIPTION_ID": "ignored-sub",
		}, valueErrors: map[string]error{"AZURE_SUBSCRIPTION_ID": errors.New("unavailable")},
			wantProjectID: "current-project"},
		{name: "both reads fail", values: map[string]string{
			"AZURE_AI_PROJECT_ID": "ignored-project", "AZURE_SUBSCRIPTION_ID": "ignored-sub",
		}, readErr: errors.New("unavailable")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			environment := &recordingEnvironmentContextReader{
				values: tt.values, valueErrors: tt.valueErrors, valuesErr: tt.readErr,
			}
			account := &recordingTenantLookup{}

			resolved := resolveEnvContextWithClients(t.Context(), "", environment, account)

			assert.Equal(t, tt.wantProjectID, resolved.projectID)
			assert.Equal(t, 1, environment.currentCalls)
			assert.Equal(t, 2, environment.getValueCalls)
			assert.Zero(t, environment.getValuesCalls)
			assert.Equal(t, []string{"default", "default"}, environment.requestedEnvironments)
			if tt.wantSubID == "" {
				assert.Empty(t, resolved.tenantID)
				assert.Empty(t, account.subscriptions)
			} else {
				assert.Equal(t, "tenant", resolved.tenantID)
				assert.Equal(t, []string{tt.wantSubID}, account.subscriptions)
			}
		})
	}
}

func TestResolveEnvContextStandaloneWithoutCurrentEnvironment(t *testing.T) {
	t.Setenv("AZURE_AI_PROJECT_ID", "process-project")
	t.Setenv("AZURE_SUBSCRIPTION_ID", "process-sub")
	for _, tt := range []struct {
		name       string
		currentErr error
		noCurrent  bool
	}{
		{name: "current lookup fails", currentErr: errors.New("unavailable")},
		{name: "no current environment", noCurrent: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			environment := &recordingEnvironmentContextReader{currentErr: tt.currentErr, noCurrent: tt.noCurrent}
			account := &recordingTenantLookup{}

			resolved := resolveEnvContextWithClients(t.Context(), "", environment, account)

			assert.Equal(t, envContext{}, resolved)
			assert.Equal(t, 1, environment.currentCalls)
			assert.Zero(t, environment.getValueCalls)
			assert.Zero(t, environment.getValuesCalls)
			assert.Empty(t, environment.requestedEnvironments)
			assert.Empty(t, account.subscriptions)
		})
	}
}

func TestResolveEnvContextWithoutDaemon(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	t.Setenv("AZURE_AI_PROJECT_ID", "process-project")
	t.Setenv("AZURE_SUBSCRIPTION_ID", "process-sub")

	assert.Equal(t, envContext{}, resolveEnvContext(t.Context(), ""))
	assert.Equal(t, envContext{}, resolveEnvContext(t.Context(), "staging"))
}

func TestResolveConnectionContextWithEnvironmentSelectsEnvironment(t *testing.T) {
	const stagingEndpoint = "https://staging.services.ai.azure.com/api/projects/staging"
	for _, tt := range []struct {
		name              string
		account           string
		project           string
		endpointKey       string
		persistedEndpoint string
		flagEndpoint      string
	}{
		{
			name: "selected persisted endpoint", account: "staging", project: "staging",
			endpointKey: "FOUNDRY_PROJECT_ENDPOINT", persistedEndpoint: stagingEndpoint,
		},
		{
			name: "selected legacy endpoint key", account: "staging", project: "staging",
			endpointKey: "AZURE_AI_PROJECT_ENDPOINT", persistedEndpoint: stagingEndpoint,
		},
		{
			// Both ARM IDs match the endpoint's names, so only the named snapshot can distinguish them.
			name: "same names but different subscription and resource group", account: "production", project: "production",
			endpointKey: "FOUNDRY_PROJECT_ENDPOINT", persistedEndpoint: connectionContextProductionEndpoint,
		},
		{
			name: "endpoint flag overrides persisted endpoint", account: "staging", project: "staging",
			endpointKey: "FOUNDRY_PROJECT_ENDPOINT", persistedEndpoint: connectionContextProductionEndpoint,
			flagEndpoint: " \thttps://STAGING.services.ai.azure.com/api/projects/staging/// \n",
		},
		{
			name: "endpoint flag works without persisted endpoint", account: "staging", project: "staging",
			flagEndpoint: stagingEndpoint,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			wantEndpoint := fmt.Sprintf("https://%s.services.ai.azure.com/api/projects/%s", tt.account, tt.project)
			markerKey := envkey.ConnectionProjectEndpoint("search")
			staging := map[string]string{
				"AZURE_SUBSCRIPTION_ID": "staging-sub",
				"AZURE_AI_PROJECT_ID": "/subscriptions/staging-sub/resourceGroups/staging-rg" +
					"/providers/Microsoft.CognitiveServices/accounts/" + tt.account + "/projects/" + tt.project,
				markerKey:         wantEndpoint,
				"UNRELATED_VALUE": "preserved",
			}
			if tt.endpointKey != "" {
				staging[tt.endpointKey] = tt.persistedEndpoint
			}
			fixture := newConnectionContextFixture(t, staging, nil)

			// Exercise real credential/client construction, but never request a token or call Azure.
			resolved, err := resolveConnectionContextWithEnvironment(fixture.ctx, tt.flagEndpoint, "staging")
			require.NoError(t, err)
			require.NotNil(t, resolved)
			assert.Equal(t, wantEndpoint, resolved.endpoint)
			assert.Equal(t, "staging-sub", resolved.sub)
			assert.Equal(t, "staging-rg", resolved.rg)
			assert.Equal(t, tt.account, resolved.account)
			assert.Equal(t, tt.project, resolved.project)
			assert.NotNil(t, resolved.armClient)
			assert.NotNil(t, resolved.dpClient)
			assert.NotNil(t, resolved.cred)
			fixture.account.mu.Lock()
			assert.Equal(t, []string{"staging-sub"}, fixture.account.subscriptions)
			assert.Equal(t, []string{connectionContextStagingTenant}, fixture.account.returnedTenants)
			fixture.account.mu.Unlock()

			wantReads := []string{"staging", "staging"} // Endpoint, then persisted ARM/tenant context.
			if tt.flagEndpoint != "" {
				wantReads = []string{"staging"} // The flag bypasses only endpoint lookup, not ARM/tenant lookup.
			}
			fixture.environment.mu.Lock()
			assert.Equal(t, wantReads, fixture.environment.client.valuesRequests)
			assert.Empty(t, fixture.environment.client.setRequests, "resolving context must not clear readiness")
			fixture.environment.mu.Unlock()

			// Exercise cleanup separately: the real delete action would perform an ARM GET first.
			require.NoError(t, invalidateDeletedConnectionMarkers(fixture.ctx, "staging", "search", resolved.endpoint))
			fixture.environment.mu.Lock()
			defer fixture.environment.mu.Unlock()
			assert.Zero(t, fixture.environment.currentCalls)
			assert.Empty(t, fixture.environment.valueRequests, "GetValue permits unsafe process fallback")
			assert.Equal(t, append(wantReads, "staging"), fixture.environment.client.valuesRequests)
			require.Len(t, fixture.environment.client.setRequests, 1)
			write := fixture.environment.client.setRequests[0]
			assert.Equal(t, "staging", write.GetEnvName())
			assert.Equal(t, markerKey, write.GetKey())
			assert.Empty(t, write.GetValue())
			wantStaging := maps.Clone(staging)
			wantStaging[markerKey] = ""
			assert.Equal(t, wantStaging, fixture.environment.client.values["staging"])
			assert.Equal(t, fixture.production, fixture.environment.client.values["production"],
				"the default environment must remain intact, even when both endpoints have identical names")
		})
	}
}

func TestResolveConnectionContextWithEnvironmentFailsClosed(t *testing.T) {
	testConnectionContextSelectionFailures(t, func(t *testing.T, ctx context.Context) error {
		resolved, err := resolveConnectionContextWithEnvironment(ctx, "", "staging")
		assert.Nil(t, resolved, "invalid selection must not produce clients for another project")
		return err
	})
}

func TestConnectionDeleteCommandFailsClosed(t *testing.T) {
	testConnectionContextSelectionFailures(t, func(_ *testing.T, ctx context.Context) error {
		extCtx := &azdext.ExtensionContext{Environment: "staging", NoPrompt: true}
		root := &cobra.Command{Use: "connection", SilenceUsage: true, SilenceErrors: true}
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		root.PersistentFlags().String("project-endpoint", "", "Foundry project endpoint")
		root.AddCommand(newConnectionDeleteCommand(extCtx))
		root.SetArgs([]string{"delete", "search", "--force"})
		return root.ExecuteContext(ctx)
	})
}

func testConnectionContextSelectionFailures(t *testing.T, run func(*testing.T, context.Context) error) {
	t.Helper()
	for _, tt := range []struct {
		name      string
		endpoints map[string]string
		readErr   error
		code      string
		message   string
	}{
		{
			name: "missing endpoint", code: exterrors.CodeMissingProjectEndpoint,
			message: `azd environment "staging" has no Foundry project endpoint`,
		},
		{
			name: "blank endpoints", code: exterrors.CodeMissingProjectEndpoint,
			endpoints: map[string]string{"FOUNDRY_PROJECT_ENDPOINT": " \t", "AZURE_AI_PROJECT_ENDPOINT": "\n"},
			message:   `azd environment "staging" has no Foundry project endpoint`,
		},
		{
			name: "invalid canonical endpoint cannot fall back to valid alias", code: exterrors.CodeInvalidParameter,
			endpoints: map[string]string{
				"FOUNDRY_PROJECT_ENDPOINT":  "http://staging.services.ai.azure.com/api/projects/staging",
				"AZURE_AI_PROJECT_ENDPOINT": connectionContextProductionEndpoint,
			},
			message: "project endpoint must use https",
		},
		{
			name: "invalid legacy endpoint", code: exterrors.CodeInvalidParameter,
			endpoints: map[string]string{"AZURE_AI_PROJECT_ENDPOINT": "https://not-foundry.example/api/projects/staging"},
			message:   "not a recognized Foundry host",
		},
		{
			name: "persisted values read fails", readErr: status.Error(codes.PermissionDenied, "staging values denied"),
			endpoints: map[string]string{"FOUNDRY_PROJECT_ENDPOINT": connectionContextProductionEndpoint},
			message:   `read project endpoint from azd environment "staging"`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			markerKey := envkey.ConnectionProjectEndpoint("search")
			staging := map[string]string{
				"AZURE_SUBSCRIPTION_ID": "staging-sub",
				"AZURE_AI_PROJECT_ID": "/subscriptions/staging-sub/resourceGroups/staging-rg" +
					"/providers/Microsoft.CognitiveServices/accounts/production/projects/production",
				markerKey: connectionContextProductionEndpoint,
			}
			maps.Copy(staging, tt.endpoints)
			fixture := newConnectionContextFixture(t, staging, tt.readErr)

			err := run(t, fixture.ctx)
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.message)
			if tt.readErr != nil {
				assert.Equal(t, codes.PermissionDenied, status.Code(err))
				assert.ErrorContains(t, err, "staging values denied")
			} else {
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok, "expected the selected environment's error, not discovery or authentication failure")
				assert.Equal(t, tt.code, localErr.Code)
				assert.NotEmpty(t, localErr.Suggestion)
				if tt.code == exterrors.CodeInvalidParameter {
					assert.Equal(t, azdext.LocalErrorCategoryValidation, localErr.Category)
				}
			}
			fixture.account.mu.Lock()
			assert.Empty(t, fixture.account.subscriptions, "stop before tenant lookup or ARM discovery")
			fixture.account.mu.Unlock()
			fixture.project.mu.Lock()
			assert.Zero(t, fixture.project.getCalls, "failed resolution must not start readiness cleanup")
			fixture.project.mu.Unlock()
			fixture.environment.mu.Lock()
			defer fixture.environment.mu.Unlock()
			assert.Zero(t, fixture.environment.currentCalls)
			assert.Empty(t, fixture.environment.valueRequests)
			assert.Equal(t, []string{"staging"}, fixture.environment.client.valuesRequests,
				"fail on the endpoint snapshot, before reading ARM context or readiness")
			assert.Empty(t, fixture.environment.client.setRequests)
			assert.Equal(t, staging, fixture.environment.client.values["staging"])
			assert.Equal(t, fixture.production, fixture.environment.client.values["production"])
		})
	}
}

const (
	connectionContextProductionEndpoint = "https://production.services.ai.azure.com/api/projects/production"
	connectionContextStagingTenant      = "22222222-2222-2222-2222-222222222222"
)

type connectionContextFixture struct {
	ctx         context.Context
	environment *connectionContextEnvironmentServer
	account     *connectionContextAccountServer
	project     *deleteReadinessProjectServer
	production  map[string]string
	hostedCalls atomic.Int32
}

func newConnectionContextFixture(t *testing.T, staging map[string]string, readErr error) *connectionContextFixture {
	t.Helper()
	// These tests change process-wide resolver state and therefore must not use t.Parallel.
	t.Setenv("NO_COLOR", "1")
	t.Setenv("AZD_ACCESS_TOKEN", "")
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", connectionContextProductionEndpoint)
	t.Setenv("AZURE_AI_PROJECT_ENDPOINT", connectionContextProductionEndpoint)
	t.Setenv("AZURE_SUBSCRIPTION_ID", "process-sub")
	t.Setenv("AZURE_AI_PROJECT_ID", "/subscriptions/process-sub/resourceGroups/process-rg"+
		"/providers/Microsoft.CognitiveServices/accounts/production/projects/production")
	// Credentials are lazy. If ARM discovery regresses, an empty PATH prevents invoking the user's azd login.
	t.Setenv("PATH", t.TempDir())
	fixture := &connectionContextFixture{
		production: map[string]string{
			"FOUNDRY_PROJECT_ENDPOINT": connectionContextProductionEndpoint,
			"AZURE_SUBSCRIPTION_ID":    "production-sub",
			"AZURE_AI_PROJECT_ID": "/subscriptions/production-sub/resourceGroups/production-rg" +
				"/providers/Microsoft.CognitiveServices/accounts/production/projects/production",
			envkey.ConnectionProjectEndpoint("search"): connectionContextProductionEndpoint,
			"UNRELATED_VALUE":                          "preserved",
		},
		account: &connectionContextAccountServer{tenants: map[string]string{
			"production-sub": "11111111-1111-1111-1111-111111111111",
			"staging-sub":    connectionContextStagingTenant,
			"process-sub":    "33333333-3333-3333-3333-333333333333",
		}},
		project: &deleteReadinessProjectServer{reader: &recordingProjectConfigReader{
			services: map[string]*azdext.ServiceConfig{"search": {Name: "search", Host: aiConnectionHost}},
		}},
	}
	fixture.environment = &connectionContextEnvironmentServer{
		deleteReadinessEnvironmentServer: &deleteReadinessEnvironmentServer{
			current: &azdext.Environment{Name: "production"},
			client: &deleteReadinessEnvironmentClient{
				recordingServiceEnvironmentClient: &recordingServiceEnvironmentClient{
					values: map[string]map[string]string{
						"production": maps.Clone(fixture.production), "staging": maps.Clone(staging),
					},
				},
				valuesErr: readErr,
			},
		},
	}
	original := projectctx.ReadAzdHostedSourcesFunc
	projectctx.ReadAzdHostedSourcesFunc = func(context.Context, string) (projectctx.AzdHostedSources, error) {
		fixture.hostedCalls.Add(1)
		// Model available default/global endpoints, but fail closed if a regression consults the cascade.
		return projectctx.AzdHostedSources{
			EnvName: "production", EnvValue: connectionContextProductionEndpoint,
			CfgFound: true, CfgState: projectctx.State{Endpoint: connectionContextProductionEndpoint},
		}, errors.New("standalone cascade must not be consulted for explicit environment selection")
	}
	t.Cleanup(func() {
		projectctx.ReadAzdHostedSourcesFunc = original
		assert.Zero(t, fixture.hostedCalls.Load(), "explicit selection must bypass default/global/process endpoint sources")
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	azdext.RegisterEnvironmentServiceServer(server, fixture.environment)
	azdext.RegisterAccountServiceServer(server, fixture.account)
	azdext.RegisterProjectServiceServer(server, fixture.project)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
		if err := <-done; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Errorf("serving connection context requests: %v", err)
		}
	})
	t.Setenv("AZD_SERVER", listener.Addr().String())
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	fixture.ctx = ctx
	return fixture
}

type connectionContextEnvironmentServer struct {
	*deleteReadinessEnvironmentServer
	valueRequests []*azdext.GetEnvRequest
}

func (s *connectionContextEnvironmentServer) GetValue(
	_ context.Context, request *azdext.GetEnvRequest,
) (*azdext.KeyValueResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.valueRequests = append(s.valueRequests, request)
	// Model the daemon's persisted-value/process fallback so a wrong API choice is observable.
	value, exists := s.client.values[request.GetEnvName()][request.GetKey()]
	if !exists {
		value = os.Getenv(request.GetKey())
	}
	return &azdext.KeyValueResponse{Value: value}, nil
}

type connectionContextAccountServer struct {
	azdext.UnimplementedAccountServiceServer
	mu              sync.Mutex
	tenants         map[string]string
	subscriptions   []string
	returnedTenants []string
}

func (s *connectionContextAccountServer) LookupTenant(
	_ context.Context, request *azdext.LookupTenantRequest,
) (*azdext.LookupTenantResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	subscription := request.GetSubscriptionId()
	s.subscriptions = append(s.subscriptions, subscription)
	tenant, exists := s.tenants[subscription]
	if !exists {
		return nil, status.Error(codes.NotFound, "unknown test subscription")
	}
	s.returnedTenants = append(s.returnedTenants, tenant)
	return &azdext.LookupTenantResponse{TenantId: tenant}, nil
}

type recordingEnvironmentContextReader struct {
	values                map[string]string
	valuesErr             error
	valueErrors           map[string]error
	currentErr            error
	noCurrent             bool
	currentCalls          int
	getValueCalls         int
	getValuesCalls        int
	requestedEnvironments []string
}

func (r *recordingEnvironmentContextReader) GetCurrent(
	context.Context,
	*azdext.EmptyRequest,
	...grpc.CallOption,
) (*azdext.EnvironmentResponse, error) {
	r.currentCalls++
	if r.currentErr != nil {
		return nil, r.currentErr
	}
	if r.noCurrent {
		return &azdext.EnvironmentResponse{}, nil
	}
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: "default"}}, nil
}

func (r *recordingEnvironmentContextReader) GetValue(
	_ context.Context,
	request *azdext.GetEnvRequest,
	_ ...grpc.CallOption,
) (*azdext.KeyValueResponse, error) {
	r.getValueCalls++
	r.requestedEnvironments = append(r.requestedEnvironments, request.GetEnvName())
	// Model daemon GetValue: missing persisted keys fall back to the process.
	value, exists := r.values[request.GetKey()]
	if !exists {
		value = os.Getenv(request.GetKey())
	}
	if err := r.valueErrors[request.GetKey()]; err != nil {
		return &azdext.KeyValueResponse{Value: value}, err
	}
	return &azdext.KeyValueResponse{Value: value}, r.valuesErr
}

func (r *recordingEnvironmentContextReader) GetValues(
	_ context.Context,
	request *azdext.GetEnvironmentRequest,
	_ ...grpc.CallOption,
) (*azdext.KeyValueListResponse, error) {
	r.getValuesCalls++
	r.requestedEnvironments = append(r.requestedEnvironments, request.GetName())
	if r.valuesErr != nil {
		return nil, r.valuesErr
	}
	response := &azdext.KeyValueListResponse{}
	for key, value := range r.values {
		response.KeyValues = append(response.KeyValues, &azdext.KeyValue{Key: key, Value: value})
	}
	return response, nil
}

type recordingTenantLookup struct {
	subscriptions []string
}

func (r *recordingTenantLookup) LookupTenant(
	_ context.Context,
	request *azdext.LookupTenantRequest,
	_ ...grpc.CallOption,
) (*azdext.LookupTenantResponse, error) {
	r.subscriptions = append(r.subscriptions, request.GetSubscriptionId())
	return &azdext.LookupTenantResponse{TenantId: "tenant"}, nil
}
