// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/foundry/connections"
	"azure.ai.toolboxes/internal/foundry/projectctx"
	"azure.ai.toolboxes/internal/pkg/azure"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

// stubToolboxConnResolver is a connectionResolver test double that returns a fixed
// project connection so buildToolEntries can be exercised without a live project.
type stubToolboxConnResolver struct {
	id     string
	target string
}

type accessConnectionResolver func(context.Context, string, string) (*projectConnection, error)

func (f accessConnectionResolver) resolveConnection(ctx context.Context, endpoint, name string) (*projectConnection, error) {
	return f(ctx, endpoint, name)
}

func stubAccessProjectEndpoint(t *testing.T) {
	t.Helper()
	previous := projectctx.ReadAzdHostedSourcesFunc
	projectctx.ReadAzdHostedSourcesFunc = func(context.Context) (projectctx.AzdHostedSources, error) {
		return projectctx.AzdHostedSources{EnvValue: accessTestEndpoint, EnvName: "test"}, nil
	}
	t.Cleanup(func() { projectctx.ReadAzdHostedSourcesFunc = previous })
}

func TestManagedDeployARMProgressBeforeAnyFoundryResolution(t *testing.T) {
	stubAccessProjectEndpoint(t)
	writes := stubToolboxEndpointEnv(t)
	var events, progress []string
	access := testToolboxAccess(t, func(request *http.Request) (*http.Response, error) {
		events = append(events, request.Method)
		if request.Method == http.MethodGet {
			return accessHTTPResponse(request, 200, map[string]any{"value": []any{}}), nil
		}
		return accessHTTPResponse(request, 201, map[string]any{}), nil
	})
	access.wait = func(_ context.Context, duration time.Duration) error {
		assert.Equal(t, 15*time.Second, duration)
		events = append(events, "settle")
		return nil
	}
	mock := newMockToolboxClient(accessTestEndpoint)
	target := &toolboxServiceTarget{
		access: access,
		newClient: func(endpoint, tenant string) (toolboxClient, error) {
			assert.Equal(t, accessTestEndpoint, endpoint)
			assert.Equal(t, "user-access-tenant", tenant)
			return accessProbeClient{toolboxClient: mock, probe: func(context.Context, string) (*azure.ToolboxObject, error) {
				events = append(events, "probe")
				return nil, notFoundResponseError("toolbox not found")
			}}, nil
		},
		resolver: accessConnectionResolver(func(_ context.Context, endpoint, name string) (*projectConnection, error) {
			assert.Equal(t, accessTestEndpoint, endpoint)
			events = append(events, "connection "+name)
			return &projectConnection{
				ID: "/connections/" + name, Name: name, Category: connections.ConnectionTypeRemoteTool,
				Target: "https://mcp.example.com",
			}, nil
		}),
	}
	props, err := structpb.NewStruct(map[string]any{
		"connections": []any{map[string]any{"name": "mcp"}},
		"tools":       []any{map[string]any{"type": "mcp", "connection": "inline"}},
	})
	require.NoError(t, err)
	_, err = target.Deploy(t.Context(), &azdext.ServiceConfig{
		Name: "research", AdditionalProperties: props, Environment: map[string]string{"SETTING": "value"},
	}, nil, nil, func(message string) { progress = append(progress, message) })
	require.NoError(t, err)
	assert.Equal(t, []string{"GET", "PUT", "settle", "probe", "connection inline", "connection mcp"}, events)
	assert.Contains(t, strings.Join(progress, "\n"), "Assigning Foundry User")
	assert.Contains(t, strings.Join(progress, "\n"), "Waiting for Foundry access")
	assert.Contains(t, strings.Join(progress, "\n"), "Foundry toolbox access is ready")
	assert.Contains(t, strings.Join(progress, "\n"), "Upserting toolbox")
	require.Len(t, mock.createVersionCalls, 1)
	require.Len(t, *writes, 1)
}

func TestManagedDeployAccessFailureSerializesGuidance(t *testing.T) {
	stubAccessProjectEndpoint(t)
	writes := stubToolboxEndpointEnv(t)
	access := testToolboxAccess(t, func(request *http.Request) (*http.Response, error) {
		return accessHTTPResponse(request, 403, map[string]any{"error": map[string]string{"code": "AuthorizationFailed"}}), nil
	})
	mock := newMockToolboxClient(accessTestEndpoint)
	mock.getResults["research"] = toolboxGetResult{err: &azcore.ResponseError{StatusCode: 403, ErrorCode: "PermissionDenied"}}
	target := &toolboxServiceTarget{
		access: access, resolver: newStubConnectionResolver(),
		newClient: func(string, string) (toolboxClient, error) { return mock, nil },
	}
	props, err := structpb.NewStruct(map[string]any{"tools": []any{map[string]any{"type": "web_search"}}})
	require.NoError(t, err)
	_, err = target.Deploy(t.Context(), &azdext.ServiceConfig{
		Name: "research", AdditionalProperties: props, Environment: map[string]string{"SETTING": "value"},
	}, nil, nil, nil)
	require.Error(t, err)
	wire := azdext.WrapError(err)
	assert.Contains(t, wire.GetMessage(), "HTTP 403")
	assert.Contains(t, wire.GetSuggestion(), "az role assignment create")
	assert.Contains(t, wire.GetSuggestion(), accessTestScope)
	assert.Empty(t, mock.createVersionCalls)
	assert.Empty(t, *writes)
}

func TestManagedDeployValidatesLocallyBeforeAuthentication(t *testing.T) {
	previous := projectctx.ReadAzdHostedSourcesFunc
	projectctx.ReadAzdHostedSourcesFunc = func(context.Context) (projectctx.AzdHostedSources, error) {
		t.Fatal("invalid definitions must fail before endpoint lookup or authentication")
		return projectctx.AzdHostedSources{}, nil
	}
	t.Cleanup(func() { projectctx.ReadAzdHostedSourcesFunc = previous })
	writes := stubToolboxEndpointEnv(t)
	for _, properties := range []map[string]any{
		{"tools": []any{map[string]any{"name": "missing-type"}}},
		{"connections": []any{map[string]any{"name": ""}}},
		{"connections": []any{map[string]any{"name": "dup"}}, "tools": []any{map[string]any{"type": "web_search", "name": "dup"}}},
		{"skills": []any{map[string]any{"name": "INVALID"}}},
		{"tools": []any{map[string]any{"type": "web_search"}}, "policies": map[string]any{"raiConfig": map[string]any{}}},
		{"tools": []any{map[string]any{"type": "mcp", "connection": "", "project_connection_id": "/connections/c"}}},
	} {
		props, err := structpb.NewStruct(properties)
		require.NoError(t, err)
		_, err = (&toolboxServiceTarget{}).Deploy(t.Context(), &azdext.ServiceConfig{
			Name: "research", AdditionalProperties: props, Environment: map[string]string{"SETTING": "value"},
		}, nil, nil, nil)
		require.Error(t, err)
		_, ok := errors.AsType[*azdext.LocalError](err)
		assert.True(t, ok)
	}
	assert.Empty(t, *writes)
}

func TestManagedReuseDoesNotPrepareAccess(t *testing.T) {
	writes := stubToolboxEndpointEnv(t)
	access := testToolboxAccess(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("reuse must not inspect or assign roles")
		return nil, nil
	})
	props, err := structpb.NewStruct(map[string]any{"endpoint": "https://mcp.example.com/reused"})
	require.NoError(t, err)
	_, err = (&toolboxServiceTarget{access: access}).Deploy(t.Context(), &azdext.ServiceConfig{
		Name: "reused.tools", AdditionalProperties: props, Environment: map[string]string{"SETTING": "value"},
	}, nil, nil, nil)
	require.NoError(t, err)
	require.Len(t, *writes, 1)
	account, ok := access.account.(*accessAccountStub)
	require.True(t, ok)
	assert.Empty(t, account.lookupSubscription)
	assert.Empty(t, account.objectSubscription)
}

func TestManagedDeployUnrelatedEndpointDoesNotBorrowTenant(t *testing.T) {
	stubToolboxEndpointEnv(t)
	const unrelatedEndpoint = "https://other.services.ai.azure.com/api/projects/other"
	previous := projectctx.ReadAzdHostedSourcesFunc
	projectctx.ReadAzdHostedSourcesFunc = func(context.Context) (projectctx.AzdHostedSources, error) {
		return projectctx.AzdHostedSources{CfgFound: true, CfgState: projectctx.State{Endpoint: unrelatedEndpoint}}, nil
	}
	t.Cleanup(func() { projectctx.ReadAzdHostedSourcesFunc = previous })
	access := testToolboxAccess(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("an unrelated endpoint must not borrow the active project role scope")
		return nil, nil
	})
	target := &toolboxServiceTarget{
		access: access, resolver: newStubConnectionResolver(),
		newClient: func(endpoint, tenant string) (toolboxClient, error) {
			assert.Equal(t, unrelatedEndpoint, endpoint)
			assert.Empty(t, tenant, "do not borrow the active project's user-access tenant")
			return newMockToolboxClient(endpoint), nil
		},
	}
	props, err := structpb.NewStruct(map[string]any{"tools": []any{map[string]any{"type": "web_search"}}})
	require.NoError(t, err)
	_, err = target.Deploy(t.Context(), &azdext.ServiceConfig{
		Name: "research", AdditionalProperties: props, Environment: map[string]string{"SETTING": "value"},
	}, nil, nil, nil)
	require.NoError(t, err)
	account, ok := access.account.(*accessAccountStub)
	require.True(t, ok)
	assert.Empty(t, account.lookupSubscription)
	assert.Empty(t, account.objectSubscription)
}

func TestManagedDeployProgressIsInvocationLocal(t *testing.T) {
	stubAccessProjectEndpoint(t)
	previous := setToolboxEndpointEnvFunc
	setToolboxEndpointEnvFunc = func(context.Context, string, string, string) error {
		return nil
	}
	t.Cleanup(func() { setToolboxEndpointEnvFunc = previous })
	access := testToolboxAccess(t, func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodGet {
			return accessHTTPResponse(request, 200, map[string]any{"value": []any{}}), nil
		}
		return accessHTTPResponse(request, 201, map[string]any{}), nil
	})
	target := &toolboxServiceTarget{
		access: access, resolver: newStubConnectionResolver(),
		newClient: func(string, string) (toolboxClient, error) { return newMockToolboxClient(accessTestEndpoint), nil },
	}
	props, err := structpb.NewStruct(map[string]any{"tools": []any{map[string]any{"type": "web_search"}}})
	require.NoError(t, err)
	type result struct {
		name     string
		messages []string
		err      error
	}
	results := make(chan result, 2)
	var workers sync.WaitGroup
	for _, name := range []string{"one", "two"} {
		workers.Go(func() {
			got := result{name: name}
			_, got.err = target.Deploy(t.Context(), &azdext.ServiceConfig{
				Name: name, AdditionalProperties: props, Environment: map[string]string{"SETTING": "value"},
			}, nil, nil, func(message string) { got.messages = append(got.messages, message) })
			results <- got
		})
	}
	workers.Wait()
	close(results)
	for got := range results {
		require.NoError(t, got.err)
		require.Len(t, got.messages, 4)
		assert.Equal(t, "Assigning Foundry User on the toolbox's Foundry project.", got.messages[0])
		assert.Equal(t, "Waiting for Foundry access to propagate.", got.messages[1])
		assert.Equal(t, "Foundry toolbox access is ready.", got.messages[2])
		assert.Equal(t, fmt.Sprintf("Upserting toolbox %q", got.name), got.messages[3])
	}
}

type stubAccountTenantLookup struct {
	request  *azdext.LookupTenantRequest
	tenantID string
	err      error
}

type stubEnvironmentReader struct {
	name            string
	subscriptionID  string
	getCurrentErr   error
	getValueErr     error
	getValueRequest *azdext.GetEnvRequest
}

func (s *stubEnvironmentReader) GetCurrent(
	context.Context,
	*azdext.EmptyRequest,
	...grpc.CallOption,
) (*azdext.EnvironmentResponse, error) {
	if s.getCurrentErr != nil {
		return nil, s.getCurrentErr
	}
	return &azdext.EnvironmentResponse{
		Environment: &azdext.Environment{Name: s.name},
	}, nil
}

func (s *stubEnvironmentReader) GetValues(
	context.Context,
	*azdext.GetEnvironmentRequest,
	...grpc.CallOption,
) (*azdext.KeyValueListResponse, error) {
	return &azdext.KeyValueListResponse{}, nil
}

func (s *stubEnvironmentReader) GetValue(
	_ context.Context,
	request *azdext.GetEnvRequest,
	_ ...grpc.CallOption,
) (*azdext.KeyValueResponse, error) {
	s.getValueRequest = request
	if s.getValueErr != nil {
		return nil, s.getValueErr
	}
	return &azdext.KeyValueResponse{Value: s.subscriptionID}, nil
}

func (s *stubAccountTenantLookup) LookupTenant(
	_ context.Context,
	request *azdext.LookupTenantRequest,
	_ ...grpc.CallOption,
) (*azdext.LookupTenantResponse, error) {
	s.request = request
	if s.err != nil {
		return nil, s.err
	}
	return &azdext.LookupTenantResponse{TenantId: s.tenantID}, nil
}

func (s stubToolboxConnResolver) resolveConnection(
	_ context.Context, _, name string,
) (*projectConnection, error) {
	return &projectConnection{ID: s.id, Name: name, Target: s.target}, nil
}

func TestParseToolboxServiceConfig_ServiceLevel(t *testing.T) {
	t.Parallel()

	props, err := structpb.NewStruct(map[string]any{
		"description": "research tools",
		"connections": []any{
			map[string]any{"name": "search", "index": "docs", "instanceName": "docs-config"},
		},
		"skills": []any{
			map[string]any{"name": "summarize", "version": "2"},
		},
		"tools": []any{
			map[string]any{"type": "web_search"},
			map[string]any{"type": "mcp", "connection": "github-mcp"},
		},
		"policies": map[string]any{
			"raiConfig": map[string]any{"raiPolicyName": "default"},
		},
		"metadata": map[string]any{"owner": "platform"},
	})
	require.NoError(t, err)

	cfg, err := parseToolboxServiceConfig(&azdext.ServiceConfig{
		Name:                 "research",
		Host:                 aiToolboxHost,
		AdditionalProperties: props,
	})
	require.NoError(t, err)
	assert.Equal(t, "research tools", cfg.Description)
	require.Len(t, cfg.Connections, 1)
	assert.Equal(t, "search", cfg.Connections[0].Name)
	assert.Equal(t, "docs", cfg.Connections[0].Index)
	assert.Equal(t, "docs-config", cfg.Connections[0].InstanceName)
	require.Len(t, cfg.Skills, 1)
	assert.Equal(t, "summarize", cfg.Skills[0].Name)
	assert.Equal(t, "2", cfg.Skills[0].Version)
	require.Len(t, cfg.Tools, 2)
	assert.Equal(t, "web_search", cfg.Tools[0]["type"])
	assert.Equal(t, "github-mcp", cfg.Tools[1]["connection"])
	require.NotNil(t, cfg.Policies)
	require.NotNil(t, cfg.Policies.RaiConfig)
	assert.Equal(t, "default", cfg.Policies.RaiConfig.RaiPolicyName)
	assert.Equal(t, map[string]string{"owner": "platform"}, cfg.Metadata)
}

func TestParseToolboxServiceConfig_RejectsLegacySnakeCaseFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		properties  map[string]any
		legacyPath  string
		replacement string
	}{
		{
			name: "connection instance",
			properties: map[string]any{
				"connections": []any{map[string]any{"name": "search", "instance_name": "docs-config"}},
			},
			legacyPath:  "connections[0].instance_name",
			replacement: "connections[0].instanceName",
		},
		{
			name: "RAI config",
			properties: map[string]any{
				"policies": map[string]any{"rai_config": map[string]any{"raiPolicyName": "default"}},
			},
			legacyPath:  "policies.rai_config",
			replacement: "policies.raiConfig",
		},
		{
			name: "RAI policy name",
			properties: map[string]any{
				"policies": map[string]any{"raiConfig": map[string]any{"rai_policy_name": "default"}},
			},
			legacyPath:  "policies.raiConfig.rai_policy_name",
			replacement: "policies.raiConfig.raiPolicyName",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			props, err := structpb.NewStruct(test.properties)
			require.NoError(t, err)

			_, err = parseToolboxServiceConfig(&azdext.ServiceConfig{
				Name:                 "research",
				Host:                 aiToolboxHost,
				AdditionalProperties: props,
			})
			localErr := requireLocalError(t, err, exterrors.CodeInvalidParameter)
			assert.Contains(t, localErr.Message, test.legacyPath)
			assert.Contains(t, localErr.Suggestion, test.replacement)
		})
	}
}

func TestParseToolboxServiceConfig_ConfigFallbackRejectsLegacySnakeCase(t *testing.T) {
	t.Parallel()

	config, err := structpb.NewStruct(map[string]any{
		"policies": map[string]any{"rai_config": map[string]any{"raiPolicyName": "default"}},
	})
	require.NoError(t, err)

	_, err = parseToolboxServiceConfig(&azdext.ServiceConfig{
		Name:   "research",
		Host:   aiToolboxHost,
		Config: config,
	})
	localErr := requireLocalError(t, err, exterrors.CodeInvalidParameter)
	assert.Contains(t, localErr.Message, "policies.rai_config")
	assert.Contains(t, localErr.Suggestion, "policies.raiConfig")
}

func TestParseToolboxServiceConfig_PreservesSnakeCaseToolPayload(t *testing.T) {
	t.Parallel()

	props, err := structpb.NewStruct(map[string]any{
		"tools": []any{
			map[string]any{
				"type": "web_search",
				"custom_search_configuration": map[string]any{
					"project_connection_id": "/connections/search",
					"instance_name":         "docs-config",
				},
			},
		},
	})
	require.NoError(t, err)

	cfg, err := parseToolboxServiceConfig(&azdext.ServiceConfig{
		Name:                 "research",
		Host:                 aiToolboxHost,
		AdditionalProperties: props,
	})
	require.NoError(t, err)
	require.Len(t, cfg.Tools, 1)
	assert.Equal(
		t,
		map[string]any{
			"project_connection_id": "/connections/search",
			"instance_name":         "docs-config",
		},
		cfg.Tools[0]["custom_search_configuration"],
	)
}

func TestParseToolboxServiceConfig_Endpoint(t *testing.T) {
	t.Parallel()

	props, err := structpb.NewStruct(map[string]any{
		"endpoint": "${RESEARCH_TOOLBOX_ENDPOINT}",
	})
	require.NoError(t, err)

	cfg, err := parseToolboxServiceConfig(&azdext.ServiceConfig{
		Name:                 "research",
		Host:                 aiToolboxHost,
		AdditionalProperties: props,
	})
	require.NoError(t, err)
	assert.Equal(t, "${RESEARCH_TOOLBOX_ENDPOINT}", cfg.Endpoint)
	assert.Empty(t, cfg.Tools)
}

func TestResolveReuseEndpoint_ExpandsVar(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"RESEARCH_TOOLBOX_ENDPOINT": "https://acct.services.ai.azure.com/api/projects/p/toolboxes/research/versions/3/mcp?api-version=v1",
	}
	cfg := &toolboxServiceConfig{Endpoint: "${RESEARCH_TOOLBOX_ENDPOINT}"}

	got, err := resolveReuseEndpoint("research", cfg, env)
	require.NoError(t, err)
	assert.Equal(t, env["RESEARCH_TOOLBOX_ENDPOINT"], got)
}

func TestResolveReuseEndpoint_PlainEndpoint(t *testing.T) {
	t.Parallel()

	cfg := &toolboxServiceConfig{Endpoint: "https://mcp.example.com/toolboxes/research/versions/1/mcp"}

	got, err := resolveReuseEndpoint("research", cfg, nil)
	require.NoError(t, err)
	assert.Equal(t, cfg.Endpoint, got)
}

func TestResolveReuseEndpoint_RejectsToolsWithEndpoint(t *testing.T) {
	t.Parallel()

	cfg := &toolboxServiceConfig{
		Endpoint: "https://mcp.example.com/toolboxes/research/versions/1/mcp",
		Tools:    []map[string]any{{"type": "web_search"}},
	}

	_, err := resolveReuseEndpoint("research", cfg, nil)
	require.Error(t, err)
}

func TestResolveReuseEndpoint_RejectsDescriptionWithEndpoint(t *testing.T) {
	t.Parallel()

	cfg := &toolboxServiceConfig{
		Endpoint:    "https://mcp.example.com/toolboxes/research/versions/1/mcp",
		Description: "reused tools",
	}

	_, err := resolveReuseEndpoint("research", cfg, nil)
	require.Error(t, err)
}

func TestResolveReuseEndpoint_RejectsEmptyResolved(t *testing.T) {
	t.Parallel()

	// ${MISSING} expands to empty when env does not define it.
	cfg := &toolboxServiceConfig{Endpoint: "${MISSING}"}

	_, err := resolveReuseEndpoint("research", cfg, map[string]string{})
	require.Error(t, err)
}

func TestPublishReuseEndpoint_WritesExpandedEndpoint(t *testing.T) {
	// No t.Parallel: stubToolboxEndpointEnv swaps a package-level seam.
	const wantURL = "https://acct.services.ai.azure.com/api/projects/p/toolboxes/research/versions/3/mcp?api-version=v1"

	calls := stubToolboxEndpointEnv(t)

	// A zero-value target proves the reuse path never builds a toolbox
	// client or creates a version (it holds no azd client or resolver).
	tgt := &toolboxServiceTarget{}
	cfg := &toolboxServiceConfig{Endpoint: "${RESEARCH_TOOLBOX_ENDPOINT}"}
	env := map[string]string{
		"RESEARCH_TOOLBOX_ENDPOINT": wantURL,
		"FOUNDRY_PROJECT_ENDPOINT":  "https://active.services.ai.azure.com/api/projects/current",
	}

	res, err := tgt.publishReuseEndpoint(t.Context(), "research", cfg, env, nil)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Len(t, *calls, 1)
	assert.Equal(t, "research", (*calls)[0].name)
	assert.Equal(t, wantURL, (*calls)[0].value)
	assert.Equal(t, "https://active.services.ai.azure.com/api/projects/current", (*calls)[0].projectScope)
}

func TestDeployReuseUsesServiceEnvironment(t *testing.T) {
	// No t.Parallel: stubToolboxEndpointEnv swaps a package-level seam.
	calls := stubToolboxEndpointEnv(t)
	const wantURL = "https://mcp.example.com/toolboxes/research"
	serviceConfig := &azdext.ServiceConfig{
		Environment: map[string]string{
			"RESEARCH_TOOLBOX_ENDPOINT": wantURL,
		},
	}

	result, err := (&toolboxServiceTarget{}).deployReuse(
		t.Context(),
		"research",
		&toolboxServiceConfig{Endpoint: "${RESEARCH_TOOLBOX_ENDPOINT}"},
		serviceConfig,
		nil,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, *calls, 1)
	assert.Equal(t, wantURL, (*calls)[0].value)
}

func TestDeployUsesSubscriptionUserTenantForCredential(t *testing.T) {
	// No t.Parallel: projectctx.ReadAzdHostedSourcesFunc and setToolboxEndpointEnvFunc are package-level seams.
	const (
		endpoint       = "https://project.services.ai.azure.com/api/projects/test"
		subscriptionID = "subscription-id"
		userTenantID   = "user-tenant-id"
	)

	previousReadSources := projectctx.ReadAzdHostedSourcesFunc
	projectctx.ReadAzdHostedSourcesFunc = func(context.Context) (projectctx.AzdHostedSources, error) {
		return projectctx.AzdHostedSources{EnvValue: endpoint, EnvName: "test"}, nil
	}
	t.Cleanup(func() { projectctx.ReadAzdHostedSourcesFunc = previousReadSources })

	const credentialErrorMessage = "failed to create Azure credential: invalid tenant ID"
	for _, tt := range []struct {
		name      string
		clientErr error
	}{
		{name: "success"},
		{name: "credential creation fails", clientErr: errors.New(credentialErrorMessage)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			endpointWrites := stubToolboxEndpointEnv(t)
			account := &stubAccountTenantLookup{tenantID: userTenantID}
			environment := &stubEnvironmentReader{name: "test", subscriptionID: subscriptionID}
			client := newMockToolboxClient(endpoint)
			var credentialTenantID string
			target := &toolboxServiceTarget{
				environmentClient: environment,
				accountClient:     account,
				resolver:          newStubConnectionResolver(),
				newClient: func(gotEndpoint, tenantID string) (toolboxClient, error) {
					assert.Equal(t, endpoint, gotEndpoint)
					credentialTenantID = tenantID
					if tt.clientErr != nil {
						return nil, tt.clientErr
					}
					return client, nil
				},
			}
			properties, err := structpb.NewStruct(map[string]any{
				"tools": []any{map[string]any{"type": "web_search"}},
			})
			require.NoError(t, err)

			result, err := target.Deploy(
				t.Context(),
				&azdext.ServiceConfig{
					Name:                 "research",
					AdditionalProperties: properties,
					Environment: map[string]string{
						"TOOLBOX_SETTING": "value",
					},
				},
				nil,
				nil,
				nil,
			)

			require.NotNil(t, environment.getValueRequest)
			assert.Equal(t, "test", environment.getValueRequest.GetEnvName())
			assert.Equal(t, "AZURE_SUBSCRIPTION_ID", environment.getValueRequest.GetKey())
			require.NotNil(t, account.request)
			assert.Equal(t, subscriptionID, account.request.GetSubscriptionId())
			assert.Equal(t, userTenantID, credentialTenantID)
			if tt.clientErr != nil {
				require.Error(t, err)
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				assert.Equal(t, exterrors.CodeCredentialCreationFailed, localErr.Code)
				assert.Equal(t, azdext.LocalErrorCategoryAuth, localErr.Category)
				assert.Equal(t, "could not create the Foundry credential", localErr.Message)
				assert.Equal(t, "run 'azd auth login' to authenticate", localErr.Suggestion)
				assert.Nil(t, result)
				assert.Empty(t, client.createVersionCalls)
				assert.Empty(t, *endpointWrites)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Len(t, client.createVersionCalls, 1)
		})
	}
}

func TestCredentialTenantIDErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		environment     *stubEnvironmentReader
		account         *stubAccountTenantLookup
		wantCode        string
		wantCategory    azdext.LocalErrorCategory
		wantLookup      bool
		wantEnvironment string
	}{
		{
			name: "active environment lookup fails",
			environment: &stubEnvironmentReader{
				getCurrentErr: errors.New("environment unavailable"),
			},
			account:      &stubAccountTenantLookup{},
			wantCode:     exterrors.CodeAzdClientFailed,
			wantCategory: azdext.LocalErrorCategoryInternal,
		},
		{
			name:         "active environment is missing",
			environment:  &stubEnvironmentReader{},
			account:      &stubAccountTenantLookup{},
			wantCode:     exterrors.CodeMissingAzureSubscription,
			wantCategory: azdext.LocalErrorCategoryDependency,
		},
		{
			name: "subscription lookup fails",
			environment: &stubEnvironmentReader{
				name:        "test",
				getValueErr: errors.New("environment unavailable"),
			},
			account:         &stubAccountTenantLookup{},
			wantCode:        exterrors.CodeAzdClientFailed,
			wantCategory:    azdext.LocalErrorCategoryInternal,
			wantEnvironment: "test",
		},
		{
			name: "subscription is missing",
			environment: &stubEnvironmentReader{
				name: "test",
			},
			account:         &stubAccountTenantLookup{},
			wantCode:        exterrors.CodeMissingAzureSubscription,
			wantCategory:    azdext.LocalErrorCategoryDependency,
			wantEnvironment: "test",
		},
		{
			name: "tenant lookup fails",
			environment: &stubEnvironmentReader{
				name:           "test",
				subscriptionID: "subscription-id",
			},
			account: &stubAccountTenantLookup{
				err: errors.New("tenant unavailable"),
			},
			wantCode:        exterrors.CodeTenantLookupFailed,
			wantCategory:    azdext.LocalErrorCategoryAuth,
			wantLookup:      true,
			wantEnvironment: "test",
		},
		{
			name: "tenant is empty",
			environment: &stubEnvironmentReader{
				name:           "test",
				subscriptionID: "subscription-id",
			},
			account:         &stubAccountTenantLookup{},
			wantCode:        exterrors.CodeTenantLookupFailed,
			wantCategory:    azdext.LocalErrorCategoryAuth,
			wantLookup:      true,
			wantEnvironment: "test",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := &toolboxServiceTarget{
				environmentClient: test.environment,
				accountClient:     test.account,
			}

			tenantID, err := target.credentialTenantID(t.Context())

			assert.Empty(t, tenantID)
			require.Error(t, err)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, test.wantCode, localErr.Code)
			assert.Equal(t, test.wantCategory, localErr.Category)
			if test.wantEnvironment != "" {
				require.NotNil(t, test.environment.getValueRequest)
				assert.Equal(t, test.wantEnvironment, test.environment.getValueRequest.GetEnvName())
			}
			if test.wantLookup {
				require.NotNil(t, test.account.request)
				assert.Equal(t, "subscription-id", test.account.request.GetSubscriptionId())
			} else {
				assert.Nil(t, test.account.request)
			}
		})
	}
}

func TestBuildToolEntries_ResolvesConnectionRef(t *testing.T) {
	t.Parallel()

	tgt := &toolboxServiceTarget{
		resolver: stubToolboxConnResolver{id: "/sub/conn/github-mcp", target: "https://mcp.example.com"},
	}
	tools := []map[string]any{
		{"type": "web_search"},
		{"type": "mcp", "connection": "github-mcp"},
	}

	out, err := tgt.buildToolEntries(context.Background(), "https://proj.example.com", tools, nil)
	require.NoError(t, err)
	require.Len(t, out, 2)

	// Non-connection tool passes through unchanged.
	assert.Equal(t, "web_search", out[0]["type"])

	// Connection-backed tool gets project_connection_id + server_url; the connection
	// name key is dropped.
	assert.Equal(t, "/sub/conn/github-mcp", out[1]["project_connection_id"])
	assert.Equal(t, "https://mcp.example.com", out[1]["server_url"])
	_, hasConnection := out[1]["connection"]
	assert.False(t, hasConnection)
}

func TestExpandToolboxValue(t *testing.T) {
	t.Parallel()

	serviceConfig := &azdext.ServiceConfig{
		Environment: map[string]string{
			"MCP_URL": "https://resolved.example.com",
		},
	}
	environment, err := (&toolboxServiceTarget{}).environmentValues(
		t.Context(),
		serviceConfig,
	)
	require.NoError(t, err)
	in := map[string]any{
		"type":       "mcp",
		"server_url": "${MCP_URL}",
		"headers":    []any{"x-secret: ${{secrets.token}}"},
	}

	out, ok := expandToolboxValue(
		in,
		environment,
	).(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "https://resolved.example.com", out["server_url"])
	// Foundry ${{...}} passes through untouched.
	assert.Equal(t, []any{"x-secret: ${{secrets.token}}"}, out["headers"])
}

// fakeServiceConfigReader reports a fixed env-declared result.
type fakeServiceConfigReader struct {
	found bool
}

func (f fakeServiceConfigReader) GetServiceConfigValue(
	context.Context,
	*azdext.GetServiceConfigValueRequest,
	...grpc.CallOption,
) (*azdext.GetServiceConfigValueResponse, error) {
	return &azdext.GetServiceConfigValueResponse{Found: f.found}, nil
}

func TestToolboxEnvironmentValuesEmptyDeclaredIsolates(t *testing.T) {
	t.Parallel()

	target := &toolboxServiceTarget{
		projectClient: fakeServiceConfigReader{found: true},
	}
	env, err := target.environmentValues(
		t.Context(),
		&azdext.ServiceConfig{Name: "research-tools"},
	)
	require.NoError(t, err)
	require.Empty(t, env)
}
