// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"azure.ai.toolboxes/internal/definition"
	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/foundry/projectctx"
	"azure.ai.toolboxes/internal/pkg/azure"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	toolboxAccessTestEndpoint = "https://acct.services.ai.azure.com/api/projects/proj"
	toolboxAccessTestID       = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/" +
		"accounts/acct/projects/proj"
)

type accessEnvironment struct {
	stubEnvironmentReader
	values map[string]string
	err    error
}

func (e *accessEnvironment) GetValues(
	context.Context, *azdext.GetEnvironmentRequest, ...grpc.CallOption,
) (*azdext.KeyValueListResponse, error) {
	if e.err != nil {
		return nil, e.err
	}
	var entries []*azdext.KeyValue
	for key, value := range e.values {
		entries = append(entries, &azdext.KeyValue{Key: key, Value: value})
	}
	return &azdext.KeyValueListResponse{KeyValues: entries}, nil
}

type accessAccount struct {
	stubAccountTenantLookup
	principalType azdext.PrincipalType
	principalID   string
	principalSub  string
}

func (a *accessAccount) GetCurrentPrincipal(
	_ context.Context, req *azdext.GetCurrentPrincipalRequest, _ ...grpc.CallOption,
) (*azdext.GetCurrentPrincipalResponse, error) {
	a.principalSub = req.GetSubscriptionId()
	return &azdext.GetCurrentPrincipalResponse{
		ObjectId: a.principalID, PrincipalType: a.principalType,
	}, nil
}

type accessRoles struct {
	events      *[]string
	assigned    bool
	assignErr   error
	principalID string
	principal   armauthorization.PrincipalType
}

func (r *accessRoles) HasAccess(context.Context, string, string) (bool, error) {
	*r.events = append(*r.events, "roles")
	return false, nil
}

func (r *accessRoles) Assign(_ context.Context, scope, id string, kind armauthorization.PrincipalType) error {
	if scope != toolboxAccessTestID {
		return errors.New("wrong scope")
	}
	*r.events = append(*r.events, "grant")
	r.assigned = r.assignErr == nil
	r.principalID, r.principal = id, kind
	return r.assignErr
}

type accessToolboxClient struct {
	*mockToolboxClient
	roles    *accessRoles
	events   *[]string
	probeErr error
}

func (c *accessToolboxClient) GetToolbox(ctx context.Context, name string) (*azure.ToolboxObject, error) {
	*c.events = append(*c.events, "read")
	if c.probeErr != nil {
		return nil, c.probeErr
	}
	if !c.roles.assigned {
		return nil, &azcore.ResponseError{StatusCode: 403, ErrorCode: "PermissionDenied"}
	}
	return c.mockToolboxClient.GetToolbox(ctx, name)
}

func (c *accessToolboxClient) CreateToolboxVersion(
	ctx context.Context, name string, req *azure.CreateToolboxVersionRequest,
) (*azure.ToolboxVersionObject, error) {
	*c.events = append(*c.events, "create")
	if !c.roles.assigned {
		return nil, &azcore.ResponseError{StatusCode: 403, ErrorCode: "PermissionDenied"}
	}
	return c.mockToolboxClient.CreateToolboxVersion(ctx, name, req)
}

type accessConnectionResolver struct {
	events *[]string
	roles  *accessRoles
}

func (r accessConnectionResolver) resolveConnection(
	_ context.Context, _, name string,
) (*projectConnection, error) {
	*r.events = append(*r.events, "connection")
	if !r.roles.assigned {
		return nil, errors.New("connection data-plane access before developer grant")
	}
	return &projectConnection{ID: "/connections/" + name, Name: name, Category: "RemoteTool", Target: "https://mcp.test"}, nil
}

func accessTestSetup(
	t *testing.T, events *[]string, kind azdext.PrincipalType,
) (*toolboxAccess, *accessToolboxClient, *accessAccount) {
	t.Helper()
	t.Setenv("AZD_AGENT_SKIP_ROLE_ASSIGNMENTS", "")
	account := &accessAccount{
		stubAccountTenantLookup: stubAccountTenantLookup{tenantID: "user-access-tenant"},
		principalType:           kind, principalID: "resource-tenant-object-id",
	}
	roles := &accessRoles{events: events}
	client := &accessToolboxClient{
		mockToolboxClient: newMockToolboxClient(toolboxAccessTestEndpoint), roles: roles, events: events,
	}
	access := &toolboxAccess{
		environment: &accessEnvironment{
			stubEnvironmentReader: stubEnvironmentReader{name: "test", subscriptionID: "sub"},
			values: map[string]string{
				"AZURE_AI_PROJECT_ID": toolboxAccessTestID, "FOUNDRY_PROJECT_ENDPOINT": toolboxAccessTestEndpoint,
				"AZURE_SUBSCRIPTION_ID": "sub",
			},
		},
		account: account,
		newCredential: func(tenantID string) (azcore.TokenCredential, error) {
			assert.Equal(t, "user-access-tenant", tenantID)
			return nil, nil
		},
		roles: roles, writer: io.Discard,
	}
	return access, client, account
}

func TestToolboxCreatePreparesAccessBeforeConnections(t *testing.T) {
	for _, kind := range []azdext.PrincipalType{
		azdext.PrincipalType_PRINCIPAL_TYPE_USER, azdext.PrincipalType_PRINCIPAL_TYPE_SERVICE_PRINCIPAL,
	} {
		t.Run(kind.String(), func(t *testing.T) {
			stubToolboxEndpointEnv(t)
			var events []string
			access, client, account := accessTestSetup(t, &events, kind)
			var diagnostics bytes.Buffer
			access.writer = &diagnostics
			prepared, err := access.writeClient(t.Context(), toolboxAccessTestEndpoint, "research",
				func(endpoint, tenantID string) (toolboxClient, error) {
					assert.Equal(t, toolboxAccessTestEndpoint, endpoint)
					assert.Equal(t, "user-access-tenant", tenantID)
					return client, nil
				})
			require.NoError(t, err)
			assert.Equal(t, "sub", account.request.GetSubscriptionId())
			assert.Equal(t, "sub", account.principalSub)
			assert.Equal(t, "resource-tenant-object-id", client.roles.principalID)
			expectedKind := armauthorization.PrincipalTypeUser
			if kind == azdext.PrincipalType_PRINCIPAL_TYPE_SERVICE_PRINCIPAL {
				expectedKind = armauthorization.PrincipalTypeServicePrincipal
			}
			assert.Equal(t, expectedKind, client.roles.principal)
			err = createToolboxFromDefinition(t.Context(), prepared,
				accessConnectionResolver{events: &events, roles: client.roles},
				toolboxAccessTestEndpoint, "research",
				&definition.Definition{Connections: []definition.ConnectionReference{{Name: "mcp"}}},
				toolboxFlags{output: "json"})
			require.NoError(t, err)
			assert.Equal(t, []string{"read", "roles", "grant", "read", "read", "connection", "create"}, events)
			assert.Equal(t, "Assigning Foundry User to the current principal.\n"+
				"Waiting for Foundry access to propagate.\nFoundry access is ready.\n", diagnostics.String())
		})
	}
}

func toolboxDeployEndpoint(t *testing.T) {
	t.Helper()
	old := projectctx.ReadAzdHostedSourcesFunc
	projectctx.ReadAzdHostedSourcesFunc = func(context.Context) (projectctx.AzdHostedSources, error) {
		return projectctx.AzdHostedSources{EnvValue: toolboxAccessTestEndpoint, EnvName: "test"}, nil
	}
	t.Cleanup(func() { projectctx.ReadAzdHostedSourcesFunc = old })
}

func TestToolboxDeployPreparesAccessWithoutAgentHook(t *testing.T) {
	stubToolboxEndpointEnv(t)
	toolboxDeployEndpoint(t)
	var events []string
	access, client, account := accessTestSetup(t, &events, azdext.PrincipalType_PRINCIPAL_TYPE_USER)
	environment, ok := access.environment.(environmentReader)
	require.True(t, ok)
	target := &toolboxServiceTarget{
		environmentClient: environment, accountClient: account, access: access,
		resolver: accessConnectionResolver{events: &events, roles: client.roles},
		newClient: func(endpoint, tenantID string) (toolboxClient, error) {
			assert.Equal(t, toolboxAccessTestEndpoint, endpoint)
			assert.Equal(t, "user-access-tenant", tenantID)
			return client, nil
		},
	}
	properties, err := structpb.NewStruct(map[string]any{
		"tools": []any{map[string]any{"type": "mcp", "connection": "${CONNECTION}"}},
	})
	require.NoError(t, err)
	_, err = target.Deploy(t.Context(), &azdext.ServiceConfig{
		Name: "research", AdditionalProperties: properties, Environment: map[string]string{"CONNECTION": "mcp"},
	}, nil, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"read", "roles", "grant", "read", "connection", "create"}, events)
	require.Len(t, client.createVersionCalls, 1)
	assert.NotContains(t, client.createVersionCalls[0].req.Tools[0], "connection")
}

func TestToolboxAccessDoesNotGrantForUnscopedSkippedOrAuthorizedOperations(t *testing.T) {
	for _, mode := range []string{"unrelated endpoint", "missing scope", "skip", "managed access", "404", "unknown 403"} {
		t.Run(mode, func(t *testing.T) {
			var events []string
			access, client, _ := accessTestSetup(t, &events, azdext.PrincipalType_PRINCIPAL_TYPE_USER)
			environment := access.environment.(*accessEnvironment)
			var diagnostics bytes.Buffer
			access.writer = &diagnostics
			switch mode {
			case "unrelated endpoint":
				environment.values["FOUNDRY_PROJECT_ENDPOINT"] = "https://other.services.ai.azure.com/api/projects/proj"
			case "missing scope":
				delete(environment.values, "AZURE_AI_PROJECT_ID")
			case "skip":
				environment.values["AZD_AGENT_SKIP_ROLE_ASSIGNMENTS"] = "true"
			case "managed access":
				client.roles.assigned = true
			case "404":
				client.probeErr = &azcore.ResponseError{StatusCode: http.StatusNotFound}
			case "unknown 403":
				client.probeErr = &azcore.ResponseError{StatusCode: http.StatusForbidden, ErrorCode: "PolicyDenied"}
			}
			_, err := access.writeClient(t.Context(), toolboxAccessTestEndpoint, "research",
				func(_, tenantID string) (toolboxClient, error) {
					if mode == "unrelated endpoint" || mode == "missing scope" {
						assert.Empty(t, tenantID, "do not borrow an unrelated environment's tenant")
					}
					return client, nil
				})
			require.NoError(t, err)
			assert.NotContains(t, events, "grant")
			assert.NotContains(t, events, "roles")
			assert.NotContains(t, diagnostics.String(), "Foundry access is ready.")
		})
	}
}

func TestToolboxAccessWarningsUseInjectedWriterWithoutCredentials(t *testing.T) {
	var events []string
	access, client, _ := accessTestSetup(t, &events, azdext.PrincipalType_PRINCIPAL_TYPE_USER)
	var diagnostics bytes.Buffer
	access.writer = &diagnostics
	client.roles.assignErr = errors.New("https://user:password@acct.services.ai.azure.com?sig=secret#fragment")
	_, err := access.writeClient(t.Context(), toolboxAccessTestEndpoint, "research",
		func(string, string) (toolboxClient, error) { return client, nil })
	require.NoError(t, err)
	assert.Contains(t, diagnostics.String(), "az role assignment create")
	assert.Contains(t, diagnostics.String(), toolboxAccessTestID)
	for _, secret := range []string{"user:password", "password", "sig=", "secret", "fragment"} {
		assert.NotContains(t, diagnostics.String(), secret)
	}
	assert.NotContains(t, diagnostics.String(), "Waiting for Foundry access")
	assert.NotContains(t, diagnostics.String(), "Foundry access is ready.")
	_, err = client.GetToolbox(t.Context(), "research")
	serviceErr, ok := errors.AsType[*azdext.ServiceError](exterrors.ServiceFromAzure(err, exterrors.OpGetToolbox))
	require.True(t, ok)
	assert.Equal(t, http.StatusForbidden, serviceErr.StatusCode)
	assert.Contains(t, serviceErr.Suggestion, "Foundry User")
}

func TestToolboxCreateAccessWarningUsesInjectedWriter(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	var diagnostics bytes.Buffer
	client, resolved, err := resolveToolboxWriteAndClient(t.Context(),
		toolboxFlags{projectEndpoint: toolboxAccessTestEndpoint, output: "json"}, "research", &diagnostics)
	require.NoError(t, err)
	assert.Equal(t, toolboxAccessTestEndpoint, client.Endpoint())
	assert.Equal(t, toolboxAccessTestEndpoint, resolved.Endpoint)
	assert.Contains(t, diagnostics.String(), "Warning:")
	assert.Contains(t, diagnostics.String(), "Foundry roles will not be assigned automatically.")
}

func TestToolboxDeployRetainsFallbackTenantWithoutMatchingScope(t *testing.T) {
	for _, mode := range []string{"missing scope", "unrelated endpoint"} {
		t.Run(mode, func(t *testing.T) {
			stubToolboxEndpointEnv(t)
			toolboxDeployEndpoint(t)
			var events []string
			access, client, account := accessTestSetup(t, &events, azdext.PrincipalType_PRINCIPAL_TYPE_USER)
			environment := access.environment.(*accessEnvironment)
			environment.subscriptionID = "fallback-subscription"
			if mode == "missing scope" {
				delete(environment.values, "AZURE_AI_PROJECT_ID")
			} else {
				environment.values["FOUNDRY_PROJECT_ENDPOINT"] = "https://other.services.ai.azure.com/api/projects/proj"
			}
			target := &toolboxServiceTarget{
				environmentClient: environment, accountClient: account, access: access,
				resolver: newStubConnectionResolver(),
				newClient: func(endpoint, tenantID string) (toolboxClient, error) {
					assert.Equal(t, toolboxAccessTestEndpoint, endpoint)
					assert.Equal(t, "user-access-tenant", tenantID)
					return client.mockToolboxClient, nil
				},
			}
			properties, err := structpb.NewStruct(map[string]any{
				"tools": []any{map[string]any{"type": "web_search"}},
			})
			require.NoError(t, err)
			_, err = target.Deploy(t.Context(), &azdext.ServiceConfig{
				Name: "research", AdditionalProperties: properties, Environment: map[string]string{"SETTING": "isolated"},
			}, nil, nil, nil)
			require.NoError(t, err)
			require.NotNil(t, account.request)
			assert.Equal(t, "fallback-subscription", account.request.GetSubscriptionId())
			assert.Empty(t, events)
			assert.Empty(t, account.principalSub)
		})
	}
}

func TestToolboxAccessRejectsMismatchedMetadataBeforeClientCreation(t *testing.T) {
	for _, key := range []string{"AZURE_AI_PROJECT_ID", "AZURE_SUBSCRIPTION_ID"} {
		t.Run(key, func(t *testing.T) {
			var events []string
			access, _, _ := accessTestSetup(t, &events, azdext.PrincipalType_PRINCIPAL_TYPE_USER)
			access.environment.(*accessEnvironment).values[key] = "invalid"
			_, err := access.writeClient(t.Context(), toolboxAccessTestEndpoint, "research",
				func(string, string) (toolboxClient, error) {
					t.Fatal("invalid metadata must fail before credential creation")
					return nil, nil
				})
			requireLocalError(t, err, exterrors.CodeInvalidParameter)
			assert.Empty(t, events)
		})
	}
}

func TestToolboxCreateValidatesLocalInputBeforeAccess(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "")
	t.Setenv("AZURE_AI_PROJECT_ENDPOINT", "")
	for _, input := range []string{
		"nonexistent.yaml",
		writeTempFile(t, ".yaml", "tools:\n  - name: missing-type\n"),
		writeTempFile(t, ".yaml", "connections:\n  - name: mcp\n    index: x\n    instanceName: y\n"),
		writeTempFile(t, ".yaml", "skills:\n  - name: INVALID\n"),
	} {
		err := runToolboxCreate(t.Context(), "research", toolboxCreateFlags{fromFile: input},
			toolboxFlags{}, nil, io.Discard)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "endpoint")
		assert.NotContains(t, err.Error(), "credential")
	}
}

func TestToolboxCreateRejectsExplicitEmptyFlags(t *testing.T) {
	for _, args := range [][]string{
		{"create", "research", "--from-file="},
		{"create", "research", "--from-file=tools.yaml", "--project-endpoint="},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root := NewRootCommand()
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.SetArgs(args)
			err := root.ExecuteContext(t.Context())
			require.Error(t, err)
			assert.Contains(t, err.Error(), "--")
		})
	}
}

type toolboxAccessEnvironmentServer struct {
	azdext.UnimplementedEnvironmentServiceServer
	reads atomic.Int32
}

func (s *toolboxAccessEnvironmentServer) GetValues(
	context.Context, *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	s.reads.Add(1)
	return &azdext.KeyValueListResponse{KeyValues: []*azdext.KeyValue{
		{Key: "FOUNDRY_PROJECT_ENDPOINT", Value: toolboxAccessTestEndpoint},
		{Key: "AZURE_AI_PROJECT_ID", Value: "invalid-project-id"},
	}}, nil
}

func TestToolboxCreateCommandReachesAccessPreparation(t *testing.T) {
	server := grpc.NewServer()
	environment := &toolboxAccessEnvironmentServer{}
	azdext.RegisterEnvironmentServiceServer(server, environment)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
	})
	t.Setenv("AZD_SERVER", listener.Addr().String())
	file := writeTempFile(t, ".yaml", "tools:\n  - type: web_search\n")
	root := NewRootCommand()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"create", "research", "--from-file=" + file, "--project-endpoint=" + toolboxAccessTestEndpoint})
	err = root.ExecuteContext(t.Context())
	local := requireLocalError(t, err, exterrors.CodeInvalidParameter)
	assert.Contains(t, local.Message, "AZURE_AI_PROJECT_ID")
	assert.Equal(t, int32(1), environment.reads.Load())
}

func TestToolboxReadAndReuseDoNotPrepareAccess(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	client, _, err := resolveToolboxAndClient(t.Context(), toolboxFlags{projectEndpoint: toolboxAccessTestEndpoint})
	require.NoError(t, err)
	assert.Equal(t, toolboxAccessTestEndpoint, client.Endpoint())

	stubToolboxEndpointEnv(t)
	properties, err := structpb.NewStruct(map[string]any{"endpoint": "https://mcp.test/toolboxes/reused"})
	require.NoError(t, err)
	target := &toolboxServiceTarget{
		access: &toolboxAccess{},
		newClient: func(string, string) (toolboxClient, error) {
			t.Fatal("reuse must not create a toolbox client")
			return nil, nil
		},
	}
	_, err = target.Deploy(t.Context(), &azdext.ServiceConfig{
		Name: "reused", AdditionalProperties: properties, Environment: map[string]string{"SETTING": "isolated"},
	}, nil, nil, nil)
	require.NoError(t, err)
}

func TestToolboxDeployRejectsInvalidInputBeforeAccess(t *testing.T) {
	toolboxDeployEndpoint(t)
	properties, err := structpb.NewStruct(map[string]any{
		"connections": []any{map[string]any{"name": "mcp"}},
		"tools":       []any{map[string]any{"name": "missing-type"}},
	})
	require.NoError(t, err)
	target := &toolboxServiceTarget{access: &toolboxAccess{}}
	_, err = target.Deploy(t.Context(), &azdext.ServiceConfig{
		Name: "research", AdditionalProperties: properties, Environment: map[string]string{"SETTING": "isolated"},
	}, nil, nil, nil)
	requireLocalError(t, err, exterrors.CodeMissingToolType)
}

func TestToolboxAccessOptionalFailuresAndCancellation(t *testing.T) {
	for _, failure := range []string{
		"environment", "credential", "tenant", "empty tenant", "cancelled environment",
		"cancelled credential", "cancelled probe", "cancelled not found",
	} {
		t.Run(failure, func(t *testing.T) {
			var events []string
			access, client, account := accessTestSetup(t, &events, azdext.PrincipalType_PRINCIPAL_TYPE_USER)
			var diagnostics bytes.Buffer
			access.writer = &diagnostics
			switch failure {
			case "environment":
				access.environment.(*accessEnvironment).err = errors.New("environment unavailable")
			case "credential", "cancelled credential":
				access.newCredential = func(string) (azcore.TokenCredential, error) {
					if failure == "cancelled credential" {
						return nil, context.Canceled
					}
					return nil, errors.New("credential unavailable")
				}
			case "tenant":
				account.err = errors.New("tenant unavailable")
			case "empty tenant":
				account.tenantID = ""
			case "cancelled environment":
				access.environment.(*accessEnvironment).err = context.Canceled
			case "cancelled probe":
				client.probeErr = context.Canceled
			case "cancelled not found":
				client.probeErr = errors.Join(context.Canceled, &azcore.ResponseError{StatusCode: http.StatusNotFound})
			}
			_, err := access.writeClient(t.Context(), toolboxAccessTestEndpoint, "research",
				func(string, string) (toolboxClient, error) { return client, nil })
			switch {
			case strings.HasPrefix(failure, "cancelled"):
				require.ErrorIs(t, err, context.Canceled)
			case failure == "tenant" || failure == "empty tenant":
				requireLocalError(t, err, exterrors.CodeTenantLookupFailed)
			default:
				require.NoError(t, err)
				assert.Contains(t, diagnostics.String(), "Warning:")
			}
			assert.NotContains(t, events, "grant")
		})
	}
}
