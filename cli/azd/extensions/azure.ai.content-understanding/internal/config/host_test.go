// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"context"
	"errors"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"azure.ai.content-understanding/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type configHost struct {
	azdext.UnimplementedEnvironmentServiceServer
	azdext.UnimplementedDeploymentServiceServer
	azdext.UnimplementedAccountServiceServer
	name     string
	values   []*azdext.KeyValue
	section  *azdext.GetConfigSectionResponse
	scope    *azdext.AzureScope
	tenant   string
	fail     string
	err      error
	block    string
	mu       sync.Mutex
	requests []string
	user     []byte
}

func validHost() *configHost {
	return &configHost{
		name: "dev",
		values: []*azdext.KeyValue{
			{Key: "CU_ENDPOINT", Value: "https://env.services.ai.azure.com"},
		},
		section: &azdext.GetConfigSectionResponse{},
		tenant:  "user-access-tenant",
		scope: &azdext.AzureScope{
			TenantId: "default-tenant", SubscriptionId: "default-sub", ResourceGroup: "default-rg",
		},
	}
}

func (h *configHost) GetUserConfig(
	ctx context.Context, request *azdext.GetUserConfigRequest,
) (*azdext.GetUserConfigResponse, error) {
	if err := h.record(ctx, "user:"+request.Path); err != nil {
		return nil, err
	}
	return &azdext.GetUserConfigResponse{Found: h.user != nil, Value: h.user}, nil
}

type userConfigHost struct {
	azdext.UnimplementedUserConfigServiceServer
	host *configHost
}

func (h *userConfigHost) Get(
	ctx context.Context, request *azdext.GetUserConfigRequest,
) (*azdext.GetUserConfigResponse, error) {
	return h.host.GetUserConfig(ctx, request)
}

func (h *configHost) record(ctx context.Context, request string) error {
	h.mu.Lock()
	h.requests = append(h.requests, request)
	h.mu.Unlock()
	md, _ := metadata.FromIncomingContext(ctx)
	if !slices.Equal(md.Get("authorization"), []string{"private-host-token"}) {
		return status.Error(codes.Unauthenticated, "private-auth-detail")
	}
	if h.block == request {
		<-ctx.Done()
		return status.FromContextError(ctx.Err()).Err()
	}
	if h.fail == request {
		return h.err
	}
	return nil
}

func (h *configHost) calls() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.requests)
}

func (h *configHost) GetCurrent(ctx context.Context, _ *azdext.EmptyRequest) (*azdext.EnvironmentResponse, error) {
	if err := h.record(ctx, "current"); err != nil {
		return nil, err
	}
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: h.name}}, nil
}

func (h *configHost) Get(ctx context.Context, request *azdext.GetEnvironmentRequest) (*azdext.EnvironmentResponse, error) {
	if err := h.record(ctx, "get:"+request.Name); err != nil {
		return nil, err
	}
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: h.name}}, nil
}

func (h *configHost) GetValues(
	ctx context.Context, request *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	if err := h.record(ctx, "values:"+request.Name); err != nil {
		return nil, err
	}
	return &azdext.KeyValueListResponse{KeyValues: h.values}, nil
}

func (h *configHost) GetConfigSection(
	ctx context.Context, request *azdext.GetConfigSectionRequest,
) (*azdext.GetConfigSectionResponse, error) {
	if err := h.record(ctx, "config:"+request.EnvName+":"+request.Path); err != nil {
		return nil, err
	}
	return h.section, nil
}

func (h *configHost) GetDeploymentContext(
	ctx context.Context, _ *azdext.EmptyRequest,
) (*azdext.GetDeploymentContextResponse, error) {
	if err := h.record(ctx, "deployment"); err != nil {
		return nil, err
	}
	return &azdext.GetDeploymentContextResponse{AzureContext: &azdext.AzureContext{Scope: h.scope}}, nil
}

func (h *configHost) LookupTenant(
	ctx context.Context, request *azdext.LookupTenantRequest,
) (*azdext.LookupTenantResponse, error) {
	if err := h.record(ctx, "tenant:"+request.SubscriptionId); err != nil {
		return nil, err
	}
	return &azdext.LookupTenantResponse{TenantId: h.tenant}, nil
}

func hostClient(t *testing.T, service *configHost) *azdext.AzdClient {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	azdext.RegisterEnvironmentServiceServer(server, service)
	azdext.RegisterDeploymentServiceServer(server, service)
	azdext.RegisterAccountServiceServer(server, service)
	azdext.RegisterUserConfigServiceServer(server, &userConfigHost{host: service})
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if err := <-done; err != nil {
			assert.ErrorIs(t, err, grpc.ErrServerStopped)
		}
	})
	t.Setenv("AZD_SERVER", listener.Addr().String())
	t.Setenv("AZD_ACCESS_TOKEN", "private-host-token")
	client, err := azdext.NewAzdClient()
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	return client
}

func TestLoadDefaultEnvironment(t *testing.T) {
	h := validHost()
	result, err := Load(azdext.WithAccessToken(t.Context(), "private-host-token"), hostClient(t, h), "", Options{}, nil)
	require.NoError(t, err)
	assert.Equal(t, "dev", result.Environment)
	assert.Equal(t, "https://env.services.ai.azure.com/contentunderstanding", result.Endpoint)
	assert.Equal(t, "default-tenant", result.TenantID)
	assert.Equal(t, ResourceScope{SubscriptionID: "default-sub", ResourceGroup: "default-rg"}, result.Scope)
	assert.Equal(t, []string{"current", "values:dev", "config:dev:cu", "user:" + UserConfigPath, "deployment"}, h.calls())
}

func TestLoadSelectedEnvironment(t *testing.T) {
	for _, tt := range []struct {
		name, explicit, processEnv, selected string
	}{
		{"explicit", "test", "", "test"},
		{"process", "", "test", "test"},
		{"explicit overrides process", "test", "other", "test"},
		{"explicit default", "dev", "", "dev"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := validHost()
			h.name = tt.selected
			h.values = append(h.values, &azdext.KeyValue{Key: "AZURE_TENANT_ID", Value: "selected-tenant"})
			h.values = append(h.values,
				&azdext.KeyValue{Key: "CU_API_VERSION", Value: "2026-06-01-preview"},
				&azdext.KeyValue{Key: "CU_DEFAULT_ANALYZER", Value: "configured"})
			ctx := azdext.WithAccessToken(t.Context(), "private-host-token")
			result, err := Load(ctx, hostClient(t, h), tt.explicit, Options{},
				map[string]string{"AZD_ENVIRONMENT": tt.processEnv})
			require.NoError(t, err)
			assert.Equal(t, tt.selected, result.Environment)
			assert.Equal(t, "selected-tenant", result.TenantID)
			assert.Equal(t, "configured", result.DefaultAnalyzer)
			assert.Equal(t, "2026-06-01-preview", result.APIVersion)
			assert.Empty(t, result.Scope.SubscriptionID)
			assert.Equal(t, []string{
				"get:" + tt.selected, "values:" + tt.selected, "config:" + tt.selected + ":cu", "user:" + UserConfigPath,
			}, h.calls())
		})
	}
}

func TestNamedEnvironmentNeverUsesDefaultDeployment(t *testing.T) {
	h := validHost()
	h.name = "test"
	_, err := Load(azdext.WithAccessToken(t.Context(), "private-host-token"), hostClient(t, h), "test", Options{}, nil)
	local, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeMissingTenant, local.Code)
	assert.Contains(t, local.Suggestion, "AZURE_TENANT_ID")
	assert.NotContains(t, h.calls(), "deployment")
}

func TestLoadProjectless(t *testing.T) {
	for _, tt := range []struct {
		name    string
		options Options
		process map[string]string
	}{
		{"explicit", Options{Endpoint: "https://explicit.services.ai.azure.com", TenantID: "explicit-tenant"}, nil},
		{"process", Options{}, map[string]string{
			"CU_ENDPOINT": "https://process.services.ai.azure.com", "AZURE_TENANT_ID": "process-tenant",
		}},
		{"mixed", Options{Endpoint: "https://explicit.services.ai.azure.com"},
			map[string]string{"AZURE_TENANT_ID": "process-tenant"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := validHost()
			ctx := azdext.WithAccessToken(t.Context(), "private-host-token")
			result, err := Load(ctx, hostClient(t, h), "", tt.options, tt.process)
			require.NoError(t, err)
			assert.Empty(t, result.Environment)
			assert.NotEmpty(t, result.TenantID)
			assert.Equal(t, "2025-11-01", result.APIVersion)
			assert.Equal(t, []string{"user:" + UserConfigPath}, h.calls())
		})
	}
}

func TestLoadProjectlessDefaultAuthentication(t *testing.T) {
	h := validHost()
	result, err := Load(azdext.WithAccessToken(t.Context(), "private-host-token"), hostClient(t, h), "",
		Options{Endpoint: "https://explicit.services.ai.azure.com"}, nil)
	require.NoError(t, err)
	assert.Empty(t, result.TenantID, "leave tenant resolution to azd authentication")
	assert.Empty(t, result.Environment)
	assert.Equal(t, []string{"user:" + UserConfigPath}, h.calls())
}

func TestLoadSubscriptionTenant(t *testing.T) {
	for _, tt := range []struct {
		name, selected, subscription string
		options                      Options
		process                      map[string]string
	}{
		{"selected environment", "test", "selected-sub", Options{}, nil},
		{"process environment selection", "", "selected-sub", Options{}, map[string]string{"AZD_ENVIRONMENT": "test"}},
		{"process subscription overrides environment", "test", "process-sub", Options{},
			map[string]string{"AZURE_SUBSCRIPTION_ID": "process-sub"}},
		{"explicit subscription overrides environment", "test", "explicit-sub",
			Options{SubscriptionID: "explicit-sub"}, nil},
		{"ARM resource overrides environment", "test", "embedded-sub", Options{Resource: accountID}, nil},
		{"projectless explicit scope", "", "explicit-sub",
			Options{Endpoint: "https://explicit.services.ai.azure.com", SubscriptionID: "explicit-sub"}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := validHost()
			h.name = "test"
			h.values = append(h.values, &azdext.KeyValue{Key: "AZURE_SUBSCRIPTION_ID", Value: "selected-sub"})
			ctx := azdext.WithAccessToken(t.Context(), "private-host-token")
			result, err := Load(ctx, hostClient(t, h), tt.selected, tt.options, tt.process)
			require.NoError(t, err)
			assert.Equal(t, "user-access-tenant", result.TenantID)
			assert.NotEqual(t, h.scope.TenantId, result.TenantID, "do not use the default deployment's resource tenant")
			assert.Equal(t, tt.subscription, result.Scope.SubscriptionID)
			expected := []string{"user:" + UserConfigPath, "tenant:" + tt.subscription}
			if result.Environment != "" {
				expected = append([]string{"get:test", "values:test", "config:test:cu"}, expected...)
			}
			assert.Equal(t, expected, h.calls())
		})
	}
}

func TestLoadConfiguredTenantSkipsSubscriptionLookup(t *testing.T) {
	for _, tt := range []struct {
		name, want string
		options    Options
		process    map[string]string
	}{
		{"environment", "environment-tenant", Options{}, nil},
		{"process", "process-tenant", Options{}, map[string]string{"AZURE_TENANT_ID": "process-tenant"}},
		{"explicit", "explicit-tenant", Options{TenantID: "explicit-tenant"},
			map[string]string{"AZURE_TENANT_ID": "process-tenant"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := validHost()
			h.values = append(h.values,
				&azdext.KeyValue{Key: "AZURE_TENANT_ID", Value: "environment-tenant"},
				&azdext.KeyValue{Key: "AZURE_SUBSCRIPTION_ID", Value: "selected-sub"},
			)
			ctx := azdext.WithAccessToken(t.Context(), "private-host-token")
			result, err := Load(ctx, hostClient(t, h), "dev", tt.options, tt.process)
			require.NoError(t, err)
			assert.Equal(t, tt.want, result.TenantID)
			assert.Equal(t, []string{"get:dev", "values:dev", "config:dev:cu", "user:" + UserConfigPath}, h.calls())
		})
	}
}

func TestLoadEmptySubscriptionTenant(t *testing.T) {
	h := validHost()
	h.tenant = ""
	h.values = append(h.values, &azdext.KeyValue{Key: "AZURE_SUBSCRIPTION_ID", Value: "selected-sub"})
	ctx := azdext.WithAccessToken(t.Context(), "private-host-token")
	result, err := Load(ctx, hostClient(t, h), "dev", Options{}, nil)
	local, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeMissingTenant, local.Code)
	assert.Equal(t, Resolved{}, result)
	assert.Equal(t, []string{
		"get:dev", "values:dev", "config:dev:cu", "user:" + UserConfigPath, "tenant:selected-sub",
	}, h.calls())
}

func TestLoadExplicitScopeAndTenant(t *testing.T) {
	for _, tt := range []struct {
		name, resource string
		process        map[string]string
		wantScope      ResourceScope
		wantDeployment bool
	}{
		{"runtime tenant", "", map[string]string{"AZURE_TENANT_ID": "process-tenant"},
			ResourceScope{SubscriptionID: "explicit-sub", ResourceGroup: "explicit-rg"}, false},
		{"runtime scope", "", nil, ResourceScope{SubscriptionID: "explicit-sub", ResourceGroup: "explicit-rg"}, true},
		{"ARM scope", accountID, nil, ResourceScope{
			SubscriptionID: "embedded-sub", ResourceGroup: "embedded-rg", ResourceName: "cu", ResourceID: accountID,
		}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := validHost()
			options := Options{SubscriptionID: "explicit-sub", ResourceGroup: "explicit-rg"}
			if tt.resource != "" {
				options = Options{Resource: tt.resource}
			}
			ctx := azdext.WithAccessToken(t.Context(), "private-host-token")
			result, err := Load(ctx, hostClient(t, h), "", options, tt.process)
			require.NoError(t, err)
			assert.Equal(t, tt.wantScope, result.Scope)
			assert.Equal(t, tt.wantDeployment, slices.Contains(h.calls(), "deployment"))
		})
	}
}

func TestLoadFailuresAreNotProjectlessFallbacks(t *testing.T) {
	for _, step := range []string{
		"current", "get:test", "values:dev", "config:dev:cu", "deployment", "tenant:selected-sub",
	} {
		for _, code := range []codes.Code{
			codes.Unknown, codes.NotFound, codes.FailedPrecondition, codes.Unauthenticated,
			codes.PermissionDenied, codes.Unavailable, codes.Canceled, codes.DeadlineExceeded,
		} {
			t.Run(step+"/"+code.String(), func(t *testing.T) {
				h := validHost()
				h.fail, h.err = step, status.Error(code, "private-host-detail")
				selected := ""
				if step == "get:test" {
					selected = "test"
				}
				if step == "tenant:selected-sub" {
					selected = "dev"
					h.values = append(h.values, &azdext.KeyValue{Key: "AZURE_SUBSCRIPTION_ID", Value: "selected-sub"})
				}
				ctx := azdext.WithAccessToken(t.Context(), "private-host-token")
				_, err := Load(ctx, hostClient(t, h), selected, Options{}, nil)
				require.Error(t, err)
				assert.NotContains(t, err.Error(), "private-host-detail")
				local, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				expected := exterrors.CodeEnvironmentUnavailable
				switch code {
				case codes.Unauthenticated, codes.PermissionDenied:
					expected = exterrors.CodeAuthenticationFailed
				case codes.Unavailable:
					expected = exterrors.CodeHostUnavailable
				case codes.Canceled:
					expected = exterrors.CodeCancelled
				case codes.DeadlineExceeded:
					expected = exterrors.CodeDeadlineExceeded
				}
				assert.Equal(t, expected, local.Code)
				calls := h.calls()
				assert.Equal(t, step, calls[len(calls)-1])
			})
		}
	}
}

func TestLoadInvalidSnapshots(t *testing.T) {
	for _, tt := range []struct {
		name, selected string
		edit           func(*configHost)
	}{
		{"missing environment identity", "", func(h *configHost) { h.name = "" }},
		{"wrong selected identity", "test", func(*configHost) {}},
		{"empty variable name", "", func(h *configHost) { h.values = append(h.values, &azdext.KeyValue{}) }},
		{"duplicate variable", "", func(h *configHost) { h.values = append(h.values, h.values[0]) }},
		{"empty found config", "", func(h *configHost) { h.section.Found = true }},
		{"malformed config", "", func(h *configHost) {
			h.section = &azdext.GetConfigSectionResponse{Found: true, Section: []byte("private-invalid-json")}
		}},
		{"missing endpoint", "", func(h *configHost) { h.values = nil }},
		{"missing deployment tenant", "", func(h *configHost) { h.scope = nil }},
		{"persisted key", "", func(h *configHost) {
			h.values = append(h.values, &azdext.KeyValue{Key: "CU_API_KEY", Value: "private-api-key"})
		}},
		{"persisted config key with runtime overrides", "dev", func(h *configHost) {
			h.section = &azdext.GetConfigSectionResponse{Found: true, Section: []byte(`{"apiKey":"private-api-key"}`)}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := validHost()
			tt.edit(h)
			options := Options{}
			if tt.selected != "" {
				options = Options{Endpoint: "https://explicit.services.ai.azure.com", TenantID: "explicit-tenant"}
			}
			ctx := azdext.WithAccessToken(t.Context(), "private-host-token")
			result, err := Load(ctx, hostClient(t, h), tt.selected, options, nil)
			require.Error(t, err)
			assert.Equal(t, Resolved{}, result, "failures must not return partial configuration")
			assert.NotContains(t, err.Error(), "private-")
		})
	}
}

func TestRuntimeKeyRejectedBeforeHostAccess(t *testing.T) {
	_, err := Load(t.Context(), nil, "", Options{}, map[string]string{"CU_API_KEY": "private-api-key"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "API-key authentication is not enabled")
	assert.NotContains(t, err.Error(), "private-api-key")
}

func TestUserDefaultsWithoutProjectOrCurrentEnvironment(t *testing.T) {
	for _, message := range []string{
		"no project exists; to create a new project, run `azd init`", "default environment not found",
	} {
		h := validHost()
		h.fail, h.err = "current", status.Error(codes.Unknown, message)
		h.user = []byte(`{"CU_ENDPOINT":"https://user.services.ai.azure.com","CU_API_VERSION":"2027-01-01-preview"}`)
		ctx := azdext.WithAccessToken(t.Context(), "private-host-token")
		result, err := Load(ctx, hostClient(t, h), "", Options{}, nil)
		require.NoError(t, err)
		assert.Empty(t, result.Environment)
		assert.Empty(t, result.TenantID)
		assert.Equal(t, "https://user.services.ai.azure.com/contentunderstanding", result.Endpoint)
		assert.Equal(t, "2027-01-01-preview", result.APIVersion)
		assert.Equal(t, "user config", result.Sources["CU_ENDPOINT"])
		assert.Equal(t, []string{"current", "user:" + UserConfigPath}, h.calls())
	}
}

func TestUserDefaultsDoNotHideExplicitEnvironmentFailures(t *testing.T) {
	h := validHost()
	h.fail = "get:missing"
	h.err = status.Error(codes.Unknown, "no project exists; to create a new project, run `azd init`")
	h.user = []byte(`{"CU_ENDPOINT":"https://user.services.ai.azure.com"}`)
	ctx := azdext.WithAccessToken(t.Context(), "private-host-token")
	_, err := Load(ctx, hostClient(t, h), "missing", Options{Endpoint: "https://flag.services.ai.azure.com"}, nil)
	require.Error(t, err)
	assert.Equal(t, []string{"get:missing"}, h.calls())
}

func TestUserDefaultsWithApplicableEnvironmentAndRuntimeEndpoint(t *testing.T) {
	for _, runtimeEndpoint := range []bool{false, true} {
		h := validHost()
		h.user = []byte(`{"CU_ENDPOINT":"https://user.services.ai.azure.com","CU_DEFAULT_ANALYZER":"prebuilt-invoice"}`)
		options := Options{}
		if runtimeEndpoint {
			options.Endpoint = "https://flag.services.ai.azure.com"
		}
		ctx := azdext.WithAccessToken(t.Context(), "private-host-token")
		result, err := Load(ctx, hostClient(t, h), "", options, nil)
		require.NoError(t, err)
		assert.Equal(t, "prebuilt-invoice", result.DefaultAnalyzer)
		assert.Equal(t, "user config", result.Sources["CU_DEFAULT_ANALYZER"])
		if runtimeEndpoint {
			assert.Empty(t, result.Environment)
			assert.Equal(t, []string{"user:" + UserConfigPath}, h.calls())
		} else {
			assert.Equal(t, "dev", result.Environment)
			assert.Equal(t, "https://env.services.ai.azure.com/contentunderstanding", result.Endpoint)
			assert.Equal(t, "azd environment: dev", result.Sources["CU_ENDPOINT"])
		}
	}
}

func TestUserConfigFailuresAreNotSilentDefaults(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `invalid`, `{"CU_ENDPOINT":15}`} {
		h := validHost()
		h.user = []byte(raw)
		h.values = append(h.values, &azdext.KeyValue{Key: "AZURE_TENANT_ID", Value: "tenant"})
		if raw == `{"CU_ENDPOINT":15}` {
			h.values = nil
		}
		ctx := azdext.WithAccessToken(t.Context(), "private-host-token")
		_, err := Load(ctx, hostClient(t, h), "", Options{}, nil)
		require.Error(t, err)
	}
	h := validHost()
	h.fail, h.err = "user:"+UserConfigPath, status.Error(codes.Unavailable, "private-user-config-detail")
	ctx := azdext.WithAccessToken(t.Context(), "private-host-token")
	_, err := Load(ctx, hostClient(t, h), "", Options{Endpoint: "https://flag.services.ai.azure.com"}, nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "private")
}

func TestLoadCancellationAndDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := Load(ctx, nil, "", Options{}, nil)
	assert.ErrorIs(t, err, context.Canceled)
	for _, step := range []string{"current", "get:dev", "values:dev", "config:dev:cu", "deployment", "tenant:selected-sub"} {
		t.Run(step, func(t *testing.T) {
			h := validHost()
			h.block = step
			client := hostClient(t, h)
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			selected := ""
			if step == "get:dev" {
				selected = "dev"
			}
			if step == "tenant:selected-sub" {
				selected = "dev"
				h.values = append(h.values, &azdext.KeyValue{Key: "AZURE_SUBSCRIPTION_ID", Value: "selected-sub"})
			}
			_, err := Load(azdext.WithAccessToken(ctx, "private-host-token"), client, selected, Options{}, nil)
			local, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, exterrors.CodeDeadlineExceeded, local.Code)
			calls := h.calls()
			require.NotEmpty(t, calls)
			assert.Equal(t, step, calls[len(calls)-1])
		})
	}
}
