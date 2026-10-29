// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const rbacTestProjectScope = "/subscriptions/sub/resourceGroups/rg/providers/" +
	"Microsoft.CognitiveServices/accounts/account/projects/project"

type rbacEnvironmentServer struct {
	azdext.UnimplementedEnvironmentServiceServer
	values map[string]string
	err    error
}

func (s *rbacEnvironmentServer) GetCurrent(
	context.Context, *azdext.EmptyRequest,
) (*azdext.EnvironmentResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: "dev"}}, nil
}

func (s *rbacEnvironmentServer) GetValue(
	_ context.Context, req *azdext.GetEnvRequest,
) (*azdext.KeyValueResponse, error) {
	return &azdext.KeyValueResponse{Value: s.values[req.Key]}, nil
}

type rbacAccountServer struct {
	azdext.UnimplementedAccountServiceServer
	principalErr error
	tenantErr    error
}

func (s *rbacAccountServer) LookupTenant(
	context.Context, *azdext.LookupTenantRequest,
) (*azdext.LookupTenantResponse, error) {
	if s.tenantErr != nil {
		return nil, s.tenantErr
	}
	return &azdext.LookupTenantResponse{TenantId: "organizations"}, nil
}

func (s *rbacAccountServer) GetCurrentPrincipal(
	context.Context, *azdext.GetCurrentPrincipalRequest,
) (*azdext.GetCurrentPrincipalResponse, error) {
	if s.principalErr != nil {
		return nil, s.principalErr
	}
	return &azdext.GetCurrentPrincipalResponse{
		ObjectId: "principal", PrincipalType: azdext.PrincipalType_PRINCIPAL_TYPE_USER,
	}, nil
}

func newRBACClient(t *testing.T, env *rbacEnvironmentServer, account *rbacAccountServer) *azdext.AzdClient {
	t.Helper()
	server := grpc.NewServer()
	azdext.RegisterEnvironmentServiceServer(server, env)
	azdext.RegisterAccountServiceServer(server, account)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if serveErr := <-done; serveErr != nil {
			require.ErrorIs(t, serveErr, grpc.ErrServerStopped)
		}
	})
	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return client
}

func TestCheckDeveloperRBACWarnings(t *testing.T) {
	t.Setenv("AZD_AGENT_SKIP_ROLE_ASSIGNMENTS", "")
	unsafeErr := fmt.Errorf("request https://user:password@registry.azurecr.io/?sig=secret#fragment: response-body-secret")
	forbidden := &azcore.ResponseError{StatusCode: http.StatusForbidden}
	tests := []struct {
		name         string
		hosted       bool
		aiMissing    bool
		assignErr    error
		roleMissing  bool
		acrMissing   bool
		abac         bool
		checkErr     int
		noEndpoint   bool
		resolveErr   bool
		resourceID   bool
		principalErr error
		want         []string
		wantCalls    []string
	}{
		{name: "sufficient hosted roles", hosted: true, wantCalls: []string{"ai", "write", "resolve", "acr"}},
		{name: "auto assignment succeeds", aiMissing: true, wantCalls: []string{"ai", "assign"}},
		{name: "auto assignment forbidden", aiMissing: true, assignErr: forbidden,
			want: []string{"auto-assign was denied"}, wantCalls: []string{"ai", "assign"}},
		{name: "auto assignment failure", aiMissing: true, assignErr: unsafeErr,
			want: []string{"auto-assign failed (non-auth error)"}, wantCalls: []string{"ai", "assign"}},
		{name: "prompt retains foundry warning", checkErr: 1,
			want: []string{"Could not check Foundry User role"}, wantCalls: []string{"ai"}},
		{name: "role write query failure", hosted: true, checkErr: 2,
			want: []string{"Could not check role-assignment-write"}, wantCalls: []string{"ai", "write", "resolve", "acr"}},
		{name: "missing role write", hosted: true, roleMissing: true,
			want: []string{"Role assignment write not available"}, wantCalls: []string{"ai", "write", "resolve", "acr"}},
		{name: "classic missing role", hosted: true, acrMissing: true,
			want: []string{"Container Registry 'registry'"}, wantCalls: []string{"ai", "write", "resolve", "acr"}},
		{name: "abac missing role", hosted: true, acrMissing: true, abac: true,
			want: []string{"ABAC-mode Container Registry 'registry'"}, wantCalls: []string{"ai", "write", "resolve", "acr"}},
		{name: "missing endpoint retains earlier warnings", hosted: true, checkErr: 1, roleMissing: true, noEndpoint: true,
			want:      []string{"Could not check Foundry User role", "Role assignment write", "ENDPOINT not set"},
			wantCalls: []string{"ai", "write"}},
		{name: "resolve failure retains earlier warnings", hosted: true, checkErr: 1, resolveErr: true,
			want:      []string{"Could not check Foundry User role", "Could not resolve ACR"},
			wantCalls: []string{"ai", "write", "resolve"}},
		{name: "targeted resolve failure", hosted: true, resourceID: true, resolveErr: true,
			want: []string{"Could not resolve ACR"}, wantCalls: []string{"ai", "write", "resolve-id"}},
		{name: "targeted resolve succeeds", hosted: true, resourceID: true,
			wantCalls: []string{"ai", "write", "resolve-id", "acr"}},
		{name: "acr query failure retains earlier warnings", hosted: true, roleMissing: true, checkErr: 3,
			want:      []string{"Role assignment write", "Could not check ACR role"},
			wantCalls: []string{"ai", "write", "resolve", "acr"}},
		{name: "all missing roles ordered", hosted: true, aiMissing: true, assignErr: forbidden,
			roleMissing: true, acrMissing: true,
			want:      []string{"auto-assign was denied", "Role assignment write", "Container Registry 'registry'"},
			wantCalls: []string{"ai", "assign", "write", "resolve", "acr"}},
		{name: "principal failure", principalErr: unsafeErr, want: []string{"Could not resolve current principal"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// #nosec G101 -- Fake credentials test non-disclosure.
			values := map[string]string{
				"AZURE_AI_PROJECT_ID":               rbacTestProjectScope,
				"AZURE_CONTAINER_REGISTRY_ENDPOINT": "https://user:password@registry.azurecr.io/?sig=secret#fragment",
			}
			if tt.noEndpoint {
				delete(values, "AZURE_CONTAINER_REGISTRY_ENDPOINT")
			}
			if tt.resourceID {
				values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = "registry-resource-id"
			}
			client := newRBACClient(t, &rbacEnvironmentServer{values: values},
				&rbacAccountServer{principalErr: tt.principalErr})
			var calls []string
			ops := developerRBACOperations{
				hasAnyRoleAssignment: func(
					_ context.Context, _ *azidentity.AzureDeveloperCLICredential,
					principal string, roles []string, scope string,
				) (bool, error) {
					assert.Equal(t, "principal", principal)
					stage, missing, index := "acr", tt.acrMissing, 3
					switch {
					case slices.Equal(roles, sufficientAIUserRoles):
						stage, missing, index = "ai", tt.aiMissing, 1
					case slices.Equal(roles, sufficientRoleAssignWriteRoles):
						stage, missing, index = "write", tt.roleMissing, 2
					default:
						expected := sufficientACRRoles
						if tt.abac {
							expected = sufficientACRAbacRoles
						}
						assert.Equal(t, expected, roles)
					}
					if stage == "acr" {
						assert.Equal(t, "registry-scope", scope)
					} else {
						assert.Equal(t, rbacTestProjectScope, scope)
					}
					calls = append(calls, stage)
					if tt.checkErr == index {
						return false, unsafeErr
					}
					return !missing, nil
				},
				assignRoleToIdentity: func(
					_ context.Context, _ *azidentity.AzureDeveloperCLICredential,
					principal, role, name, scope string, kind armauthorization.PrincipalType,
				) (bool, error) {
					calls = append(calls, "assign")
					assert.Equal(t, "principal", principal)
					assert.Equal(t, roleAzureAIUser, role)
					assert.Equal(t, "Foundry User → Foundry Project", name)
					assert.Equal(t, rbacTestProjectScope, scope)
					assert.Equal(t, armauthorization.PrincipalTypeUser, kind)
					return true, tt.assignErr
				},
			}
			resolve := func() (string, bool, error) {
				if tt.resolveErr {
					return "", false, unsafeErr
				}
				return "registry-scope", tt.abac, nil
			}
			ops.resolveACRInfo = func(
				_ context.Context, _ *azidentity.AzureDeveloperCLICredential, subscription, endpoint string,
			) (string, bool, error) {
				calls = append(calls, "resolve")
				assert.Equal(t, "sub", subscription)
				assert.Equal(t, values["AZURE_CONTAINER_REGISTRY_ENDPOINT"], endpoint)
				return resolve()
			}
			ops.resolveACRInfoByResourceID = func(
				_ context.Context, _ *azidentity.AzureDeveloperCLICredential, id string,
			) (string, bool, error) {
				calls = append(calls, "resolve-id")
				assert.Equal(t, "registry-resource-id", id)
				return resolve()
			}
			messages, err := checkDeveloperRBAC(t.Context(), client, tt.hosted, ops)
			require.NoError(t, err)
			require.Equal(t, tt.wantCalls, calls)
			require.Len(t, messages, len(tt.want))
			for i, message := range messages {
				assert.Equal(t, azdext.BetaServiceEventMessageWarning, message.Kind)
				assert.Contains(t, message.Message, tt.want[i])
				assert.NotEmpty(t, message.Suggestion)
				text := message.Message + message.Suggestion
				for _, secret := range []string{"user:", "password", "sig=", "secret", "fragment", "\x1b", "WARNING:"} {
					assert.NotContains(t, text, secret)
				}
			}
			if tt.acrMissing {
				suggestion := messages[len(messages)-1].Suggestion
				if tt.abac {
					assert.Contains(t, suggestion, `--role "Container Registry Repository Writer"`)
					assert.NotContains(t, suggestion, "AcrPush")
				} else {
					assert.Contains(t, suggestion, `--role "AcrPush"`)
				}
				assert.Contains(t, suggestion, `--scope "registry-scope"`)
			}
			if tt.aiMissing && errors.Is(tt.assignErr, forbidden) {
				assert.Contains(t, messages[0].Suggestion, "--role "+roleAzureAIUser)
				assert.Contains(t, messages[0].Suggestion, fmt.Sprintf("--scope %q", rbacTestProjectScope))
			}
			if tt.roleMissing {
				for _, message := range messages {
					if strings.Contains(message.Message, "Role assignment write") {
						assert.Contains(t, message.Suggestion, `--role "Role Based Access Control Administrator"`)
						assert.Contains(t, message.Suggestion, fmt.Sprintf("--scope %q", rbacTestProjectScope))
						assert.Contains(t, message.Suggestion, "AZD_AGENT_SKIP_ROLE_ASSIGNMENTS=true")
					}
				}
			}
		})
	}
}

func TestDeveloperRBACSafeDiagnostics(t *testing.T) {
	responseErr := &azcore.ResponseError{
		StatusCode: http.StatusForbidden, ErrorCode: "response-body-secret",
		RawResponse: &http.Response{
			Body: http.NoBody,
		},
	}
	assert.Equal(t, "HTTP 403", developerRBACErrorSummary(fmt.Errorf("secret: %w", responseErr)))
	assert.Equal(t, "PermissionDenied", developerRBACErrorSummary(status.Error(codes.PermissionDenied, "secret")))
	assert.Equal(t, "the operation failed", developerRBACErrorSummary(errors.New("secret")))
	for _, tt := range []struct{ endpoint, want string }{
		{"https://user:password@Registry.azurecr.io/?sig=secret#fragment", "registry"},
		{"user:password@registry.azurecr.io?sig=secret#fragment", "registry"},
		{"https://user:%invalid@registry.azurecr.io", "(configured registry)"},
	} {
		assert.Equal(t, tt.want, developerRBACRegistryName(tt.endpoint))
	}
}

func TestCheckDeveloperRBACSkipsAndErrors(t *testing.T) {
	for _, tt := range []struct {
		name, envSkip, processSkip, projectID string
		envErr, tenantErr                     error
		wantErr                               bool
	}{
		{name: "environment skip true", envSkip: "true", projectID: rbacTestProjectScope},
		{name: "numeric skip", envSkip: "1", projectID: rbacTestProjectScope},
		{name: "process fallback", processSkip: "TRUE", projectID: rbacTestProjectScope},
		{name: "environment false takes precedence", envSkip: "false", processSkip: "true", projectID: rbacTestProjectScope},
		{name: "invalid environment takes precedence", envSkip: "invalid", processSkip: "true",
			projectID: rbacTestProjectScope},
		{name: "missing project"},
		{name: "invalid project", projectID: "invalid"},
		{name: "tenant unavailable", projectID: rbacTestProjectScope, tenantErr: status.Error(codes.NotFound, "tenant")},
		{name: "environment fatal error", envErr: status.Error(codes.Unavailable, "environment"), wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AZD_AGENT_SKIP_ROLE_ASSIGNMENTS", tt.processSkip)
			client := newRBACClient(t, &rbacEnvironmentServer{err: tt.envErr, values: map[string]string{
				"AZD_AGENT_SKIP_ROLE_ASSIGNMENTS": tt.envSkip, "AZURE_AI_PROJECT_ID": tt.projectID,
			}}, &rbacAccountServer{tenantErr: tt.tenantErr})
			var checked bool
			ops := developerRBACOperations{hasAnyRoleAssignment: func(
				context.Context, *azidentity.AzureDeveloperCLICredential, string, []string, string,
			) (bool, error) {
				checked = true
				return true, nil
			}}
			messages, err := checkDeveloperRBAC(t.Context(), client, false, ops)
			require.Empty(t, messages)
			if tt.wantErr {
				require.ErrorContains(t, err, "failed to get current environment")
				assert.Equal(t, codes.Unavailable, status.Code(err))
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, strings.Contains(tt.name, "takes precedence"), checked)
		})
	}
}

func TestCheckDeveloperRBACConcurrentInvocations(t *testing.T) {
	t.Setenv("AZD_AGENT_SKIP_ROLE_ASSIGNMENTS", "")
	client := newRBACClient(t, &rbacEnvironmentServer{values: map[string]string{
		"AZURE_AI_PROJECT_ID": rbacTestProjectScope,
	}}, &rbacAccountServer{principalErr: status.Error(codes.PermissionDenied, "denied")})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			messages, err := CheckDeveloperRBAC(t.Context(), client, true)
			assert.NoError(t, err)
			if assert.Len(t, messages, 1) {
				assert.Contains(t, messages[0].Message, "PermissionDenied")
			}
		})
	}
	wg.Wait()
}

type developerRBACAccountServer struct {
	azdext.UnimplementedAccountServiceServer
}

func (s *developerRBACAccountServer) GetCurrentPrincipal(
	_ context.Context, req *azdext.GetCurrentPrincipalRequest,
) (*azdext.GetCurrentPrincipalResponse, error) {
	if req.GetSubscriptionId() != "selected-subscription" {
		return nil, status.Error(codes.InvalidArgument, "subscription is required")
	}
	return &azdext.GetCurrentPrincipalResponse{
		ObjectId: "resource-tenant-object-id", PrincipalType: azdext.PrincipalType_PRINCIPAL_TYPE_USER,
	}, nil
}

func TestDeveloperRBACPrincipalUsesStableRPC(t *testing.T) {
	methods := make(chan string, 2)
	server := grpc.NewServer(grpc.UnaryInterceptor(
		func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			methods <- info.FullMethod
			return handler(ctx, req)
		},
	))
	azdext.RegisterAccountServiceServer(server, &developerRBACAccountServer{})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(client.Close)

	id, principalType, err := developerRBACPrincipal(t.Context(), client.Account(), "selected-subscription")
	require.NoError(t, err)
	assert.Equal(t, "resource-tenant-object-id", id)
	assert.Equal(t, armauthorization.PrincipalTypeUser, principalType)
	assert.Equal(t, "/azd.extensions.v1.AccountService/GetCurrentPrincipal", <-methods)

	_, _, err = developerRBACPrincipal(t.Context(), client.Account(), "")
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Equal(t, "/azd.extensions.v1.AccountService/GetCurrentPrincipal", <-methods)
}

type developerRBACAccountClient struct {
	azdext.AccountServiceClient
	response       *azdext.GetCurrentPrincipalResponse
	err            error
	subscriptionID string
}

func (c *developerRBACAccountClient) GetCurrentPrincipal(
	_ context.Context, req *azdext.GetCurrentPrincipalRequest, _ ...grpc.CallOption,
) (*azdext.GetCurrentPrincipalResponse, error) {
	c.subscriptionID = req.GetSubscriptionId()
	return c.response, c.err
}

func TestDeveloperRBACPrincipal(t *testing.T) {
	lookupErr := status.Error(codes.PermissionDenied, "subscription unavailable")
	tests := []struct {
		name     string
		response *azdext.GetCurrentPrincipalResponse
		err      error
		wantType armauthorization.PrincipalType
		wantErr  string
	}{
		{
			name: "resource tenant guest user",
			response: &azdext.GetCurrentPrincipalResponse{
				ObjectId: "resource-tenant-object-id", PrincipalType: azdext.PrincipalType_PRINCIPAL_TYPE_USER,
			},
			wantType: armauthorization.PrincipalTypeUser,
		},
		{
			name: "service principal",
			response: &azdext.GetCurrentPrincipalResponse{
				ObjectId: "service-principal-object-id", PrincipalType: azdext.PrincipalType_PRINCIPAL_TYPE_SERVICE_PRINCIPAL,
			},
			wantType: armauthorization.PrincipalTypeServicePrincipal,
		},
		{name: "lookup failure", err: lookupErr, wantErr: "subscription unavailable"},
		{name: "empty ID", response: &azdext.GetCurrentPrincipalResponse{}, wantErr: "empty current principal object ID"},
		{
			name: "unsupported type", response: &azdext.GetCurrentPrincipalResponse{ObjectId: "object-id"},
			wantErr: "unsupported current principal type",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := &developerRBACAccountClient{response: tt.response, err: tt.err}
			id, principalType, err := developerRBACPrincipal(t.Context(), account, "selected-subscription")
			assert.Equal(t, "selected-subscription", account.subscriptionID)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				assert.Empty(t, id)
				assert.Empty(t, principalType)
				if tt.err != nil {
					assert.ErrorIs(t, err, tt.err)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.response.ObjectId, id)
			assert.Equal(t, tt.wantType, principalType)
		})
	}
}

func TestNormalizeLoginServer(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain login server", "crfoo.azurecr.io", "crfoo.azurecr.io"},
		{"with https prefix", "https://crfoo.azurecr.io", "crfoo.azurecr.io"},
		{"with http prefix", "http://crfoo.azurecr.io", "crfoo.azurecr.io"},
		{"with trailing slash", "crfoo.azurecr.io/", "crfoo.azurecr.io"},
		{"uppercase domain", "CrFoo.AzureCR.io", "crfoo.azurecr.io"},
		{"uppercase HTTPS prefix", "HTTPS://crfoo.azurecr.io", "crfoo.azurecr.io"},
		{"mixed case prefix and domain", "Https://CrFoo.AzureCR.io/", "crfoo.azurecr.io"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeLoginServer(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDeveloperRBACRoleConstants(t *testing.T) {
	// ACR roles
	assert.Equal(t, "fb382eab-e894-4461-af04-94435c366c3f", roleContainerRegistryTasksContributor)
	assert.Equal(t, "2efddaa5-3f1f-4df3-97df-af3f13818f4c", roleContainerRegistryRepositoryContributor)
	assert.Equal(t, "8311e382-0749-4cb8-b61a-304f252e45ec", roleAcrPush)
	assert.Equal(t, "2a1e307c-b015-4ebd-883e-5b7698a07328", roleAcrRepositoryWriter)

	// Superset roles
	assert.Equal(t, "8e3af657-a8ff-443c-a75c-2fe8c4bcb635", roleOwner)
	assert.Equal(t, "b24988ac-6180-42a0-ab88-20f7382dd24c", roleContributor)

	// Role-assignment write roles
	assert.Equal(t, "18d7d88d-d35e-4fb5-a5c3-7773c20a72d9", roleUserAccessAdministrator)
	assert.Equal(t, "f58310d9-a9f6-439a-9e8d-f62e7b41a168", roleRBACAdministrator)
	assert.Equal(t, "eadc314b-1a2d-4efa-be10-5d325db5065e", roleAzureAIProjectManager)
	assert.Equal(t, "e47c6f54-e4a2-4754-9501-8e0985b135e1", roleAzureAIAccountOwner)

	// AI roles
	assert.Equal(t, "64702f94-c441-49e6-a78b-ef80e0188fee", roleAzureAIDeveloper)
	assert.Equal(t, "a97b65f3-24c7-4388-baec-2e87135dc908", roleCognitiveServicesUser)
	assert.Equal(t, "c883944f-8b7b-4483-af10-35834be79c4a", roleFoundryOwner)
}

func TestSufficientRoleLists(t *testing.T) {
	// Verify the sufficient role lists contain expected entries.
	assert.Contains(t, sufficientACRRoles, roleOwner)
	assert.Contains(t, sufficientACRRoles, roleContributor)
	assert.Contains(t, sufficientACRRoles, roleAcrPush)
	assert.Contains(t, sufficientACRRoles, roleContainerRegistryTasksContributor)
	assert.Contains(t, sufficientACRRoles, roleContainerRegistryRepositoryContributor)

	// sufficientAIUserRoles must contain only roles whose dataActions cover AIServices.*
	assert.Contains(t, sufficientAIUserRoles, roleAzureAIUser)           // Foundry User: CogSvc.*
	assert.Contains(t, sufficientAIUserRoles, roleCognitiveServicesUser) // backward compat: CogSvc.*
	assert.Contains(t, sufficientAIUserRoles, roleAzureAIProjectManager) // Foundry Project Manager: CogSvc.*
	assert.Contains(t, sufficientAIUserRoles, roleFoundryOwner)          // Foundry Owner: CogSvc.*
	// Owner/Contributor have no dataActions and must NOT be treated as sufficient
	assert.NotContains(t, sufficientAIUserRoles, roleOwner)
	assert.NotContains(t, sufficientAIUserRoles, roleContributor)
	// Azure AI Developer only covers OpenAI/Speech/ContentSafety/MaaS — NOT AIServices.*
	assert.NotContains(t, sufficientAIUserRoles, roleAzureAIDeveloper)

	// Role-assignment write: Owner, UAA, RBAC Admin, Foundry Project Manager, Foundry Account Owner;
	// Contributor must NOT be included.
	assert.Contains(t, sufficientRoleAssignWriteRoles, roleOwner)
	assert.Contains(t, sufficientRoleAssignWriteRoles, roleUserAccessAdministrator)
	assert.Contains(t, sufficientRoleAssignWriteRoles, roleRBACAdministrator)
	assert.Contains(t, sufficientRoleAssignWriteRoles, roleAzureAIProjectManager)
	assert.Contains(t, sufficientRoleAssignWriteRoles, roleAzureAIAccountOwner)
	assert.Contains(t, sufficientRoleAssignWriteRoles, roleFoundryOwner) // Foundry Owner has roleAssignments/write
	assert.NotContains(t, sufficientRoleAssignWriteRoles, roleContributor)

	// ABAC ACR roles: Owner, RepositoryWriter, RepositoryContributor; AcrPush must NOT be included.
	assert.Contains(t, sufficientACRAbacRoles, roleOwner)
	assert.Contains(t, sufficientACRAbacRoles, roleAcrRepositoryWriter)
	assert.Contains(t, sufficientACRAbacRoles, roleContainerRegistryRepositoryContributor)
	assert.NotContains(t, sufficientACRAbacRoles, roleAcrPush)
}
