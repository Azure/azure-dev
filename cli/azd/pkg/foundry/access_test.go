// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const accessTestScope = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/" +
	"accounts/acct/projects/proj"

func TestProjectAccessScope(t *testing.T) {
	const endpoint = "https://acct.services.ai.azure.com/api/projects/proj"
	for _, tt := range []struct {
		name     string
		id       string
		endpoint string
		valid    bool
	}{
		{name: "matching project", id: accessTestScope, endpoint: endpoint, valid: true},
		{name: "trailing slash", id: accessTestScope, endpoint: endpoint + "/", valid: true},
		{name: "case insensitive ARM ID", id: strings.ToUpper(accessTestScope), endpoint: endpoint, valid: true},
		{name: "invalid ID", id: "/subscriptions/sub", endpoint: endpoint},
		{name: "missing ID", endpoint: endpoint},
		{name: "child resource", id: accessTestScope + "/agents/agent", endpoint: endpoint},
		{name: "missing endpoint", id: accessTestScope},
		{name: "different account", id: accessTestScope, endpoint: strings.Replace(endpoint, "acct.", "other.", 1)},
		{name: "different project", id: accessTestScope, endpoint: endpoint + "-other"},
		{name: "different host", id: accessTestScope, endpoint: "https://unrelated.example/api/projects/proj"},
		{name: "insecure endpoint", id: accessTestScope, endpoint: strings.Replace(endpoint, "https:", "http:", 1)},
		{name: "credentials", id: accessTestScope,
			endpoint: strings.Replace(endpoint, "https://", "https://user:secret@", 1)},
		{name: "query credentials", id: accessTestScope, endpoint: endpoint + "?sig=secret"},
		{name: "fragment credentials", id: accessTestScope, endpoint: endpoint + "#secret"},
		{name: "custom port", id: accessTestScope, endpoint: strings.Replace(endpoint, ".com/", ".com:8080/", 1)},
		{name: "malformed URL", id: accessTestScope, endpoint: endpoint + "%secret"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resource, err := ProjectAccessScope(tt.id, tt.endpoint)
			if !tt.valid {
				require.Error(t, err)
				assert.Nil(t, resource)
				assert.NotContains(t, err.Error(), "secret")
				if tt.endpoint != "" {
					assert.NotContains(t, err.Error(), tt.endpoint, "errors must not disclose authored URLs")
				}
				return
			}
			require.NoError(t, err)
			assert.True(t, strings.EqualFold(accessTestScope, resource.String()))
		})
	}
}

func TestProjectAccessRolesAreIndependent(t *testing.T) {
	roles := ProjectAccessRoles()
	require.Len(t, roles, 4)
	roles[0] = "changed"
	assert.Equal(t, FoundryUserRole, ProjectAccessRoles()[0])
}

func TestProjectScopeFromEnvironment(t *testing.T) {
	const endpoint = "https://acct.services.ai.azure.com/api/projects/proj"
	for _, tt := range []struct {
		name     string
		endpoint string
		change   func(map[string]string)
		wantNil  bool
		wantErr  string
	}{
		{name: "active project"},
		{name: "legacy endpoint key", change: func(v map[string]string) {
			v["AZURE_AI_PROJECT_ENDPOINT"] = v["FOUNDRY_PROJECT_ENDPOINT"]
			delete(v, "FOUNDRY_PROJECT_ENDPOINT")
		}},
		{name: "whitespace Foundry key falls back to legacy endpoint", change: func(v map[string]string) {
			v["AZURE_AI_PROJECT_ENDPOINT"] = v["FOUNDRY_PROJECT_ENDPOINT"]
			v["FOUNDRY_PROJECT_ENDPOINT"] = "   "
		}},
		{name: "Foundry key wins", change: func(v map[string]string) {
			v["AZURE_AI_PROJECT_ENDPOINT"] = "https://other.services.ai.azure.com/api/projects/proj"
		}},
		{name: "unrelated explicit endpoint",
			endpoint: "https://other.services.ai.azure.com/api/projects/proj", wantNil: true},
		{name: "explicit endpoint overrides invalid default", change: func(v map[string]string) {
			v["FOUNDRY_PROJECT_ENDPOINT"] = "invalid-default"
		}, wantNil: true},
		{name: "missing project ID", change: func(v map[string]string) { delete(v, "AZURE_AI_PROJECT_ID") }, wantNil: true},
		{name: "missing environment endpoint", change: func(v map[string]string) {
			delete(v, "FOUNDRY_PROJECT_ENDPOINT")
		}, wantNil: true},
		{name: "malformed project ID", change: func(v map[string]string) {
			v["AZURE_AI_PROJECT_ID"] = "/subscriptions/sub"
		}, wantErr: "not a valid Foundry project"},
		{name: "wrong resource type", change: func(v map[string]string) {
			v["AZURE_AI_PROJECT_ID"] = strings.ReplaceAll(accessTestScope, "/projects/", "/connections/")
		}, wantErr: "not a valid Foundry project"},
		{name: "contradictory subscription", change: func(v map[string]string) {
			v["AZURE_SUBSCRIPTION_ID"] = "other"
		}, wantErr: "conflicts"},
		{name: "contradictory project", change: func(v map[string]string) {
			v["AZURE_AI_PROJECT_ID"] = accessTestScope + "-other"
		}, wantErr: "does not identify"},
		{name: "contradictory account", change: func(v map[string]string) {
			v["AZURE_AI_PROJECT_ID"] = strings.ReplaceAll(accessTestScope, "/acct/", "/other/")
		}, wantErr: "does not identify"},
		{name: "case-insensitive ARM identity", change: func(v map[string]string) {
			v["AZURE_AI_PROJECT_ID"] = strings.ToUpper(accessTestScope)
		}},
		{name: "normalized endpoint", endpoint: "  https://ACCT.services.ai.azure.com/api/projects/proj/  "},
		{name: "credential-bearing default", change: func(v map[string]string) {
			v["FOUNDRY_PROJECT_ENDPOINT"] = "https://user:secret@acct.services.ai.azure.com/api/projects/proj?sig=secret"
		}, wantNil: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			values := map[string]string{
				"AZURE_AI_PROJECT_ID": accessTestScope, "FOUNDRY_PROJECT_ENDPOINT": endpoint, "AZURE_SUBSCRIPTION_ID": "sub",
			}
			if tt.change != nil {
				tt.change(values)
			}
			selected := tt.endpoint
			if selected == "" {
				selected = endpoint
			}
			scope, err := ProjectScopeFromEnvironment(selected, values)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				assert.Nil(t, scope)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantNil, scope == nil)
			}
		})
	}
}

func TestRoleAssignmentsSkipped(t *testing.T) {
	for _, tt := range []struct {
		name, env, process string
		want               bool
	}{
		{name: "default"},
		{name: "environment true", env: "true", want: true},
		{name: "process true", process: "true", want: true},
		{name: "environment false overrides process", env: "false", process: "true"},
		{name: "invalid is not enabled", env: "invalid", process: "true"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, RoleAssignmentsSkipped(tt.env, tt.process))
		})
	}
}

type accessTestEnvironment struct {
	err     error
	request *azdext.GetEnvironmentRequest
}

func (e *accessTestEnvironment) GetValues(
	_ context.Context, req *azdext.GetEnvironmentRequest, _ ...grpc.CallOption,
) (*azdext.KeyValueListResponse, error) {
	e.request = req
	return &azdext.KeyValueListResponse{KeyValues: []*azdext.KeyValue{{Key: "SETTING", Value: "value"}}}, e.err
}

func TestLoadProjectEnvironment(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
	}{
		{name: "active environment"},
		{name: "no active environment", err: errors.New("no active environment")},
		{name: "lookup failure", err: errors.New("unavailable")},
		{name: "cancellation", err: context.Canceled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			environment := &accessTestEnvironment{err: tt.err}
			values, err := LoadProjectEnvironment(t.Context(), environment)
			require.NotNil(t, environment.request)
			assert.Empty(t, environment.request.GetName(), "the host must resolve the active environment")
			if tt.err != nil {
				require.ErrorContains(t, err, "read the active azd environment")
				assert.ErrorIs(t, err, tt.err)
				assert.Nil(t, values)
			} else {
				require.NoError(t, err)
				assert.Equal(t, map[string]string{"SETTING": "value"}, values)
			}
		})
	}
}

type accessTestTenant struct {
	tenantID string
	err      error
	request  *azdext.LookupTenantRequest
}

func (a *accessTestTenant) LookupTenant(
	_ context.Context, req *azdext.LookupTenantRequest, _ ...grpc.CallOption,
) (*azdext.LookupTenantResponse, error) {
	a.request = req
	return &azdext.LookupTenantResponse{TenantId: a.tenantID}, a.err
}

func TestLookupProjectTenant(t *testing.T) {
	for _, tt := range []struct {
		name, tenant string
		err          error
		wantErr      string
	}{
		{name: "user-access tenant", tenant: "  user-access-tenant  "},
		{name: "empty tenant", wantErr: "empty user-access tenant"},
		{name: "lookup failure", err: errors.New("unavailable"), wantErr: "unavailable"},
		{name: "cancellation", err: context.Canceled, wantErr: "canceled"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			account := &accessTestTenant{tenantID: tt.tenant, err: tt.err}
			tenantID, err := LookupProjectTenant(t.Context(), account, "selected-subscription")
			require.NotNil(t, account.request)
			assert.Equal(t, "selected-subscription", account.request.GetSubscriptionId())
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				assert.Empty(t, tenantID)
				if tt.err != nil {
					assert.ErrorIs(t, err, tt.err)
				}
			} else {
				require.NoError(t, err)
				assert.Equal(t, "user-access-tenant", tenantID)
			}
		})
	}
}

type accessTestAccount struct {
	response *azdext.GetCurrentPrincipalResponse
	err      error
	calls    int
}

func (a *accessTestAccount) GetCurrentPrincipal(
	_ context.Context, req *azdext.GetCurrentPrincipalRequest, _ ...grpc.CallOption,
) (*azdext.GetCurrentPrincipalResponse, error) {
	a.calls++
	if req.SubscriptionId != "sub" {
		return nil, errors.New("wrong subscription")
	}
	return a.response, a.err
}

type accessTestRoles struct {
	hasAccess   bool
	checkErr    error
	assignErr   error
	checks      int
	assigns     int
	principalID string
	principal   armauthorization.PrincipalType
}

func (r *accessTestRoles) HasAccess(_ context.Context, _, principalID string) (bool, error) {
	r.checks++
	r.principalID = principalID
	return r.hasAccess, r.checkErr
}

func (r *accessTestRoles) Assign(
	_ context.Context, _, principalID string, principal armauthorization.PrincipalType,
) error {
	r.assigns++
	r.principalID, r.principal = principalID, principal
	return r.assignErr
}

func accessDenied() error {
	return &azcore.ResponseError{StatusCode: http.StatusForbidden, ErrorCode: "PermissionDenied"}
}

func TestAccessPreparation(t *testing.T) {
	for _, tt := range []struct {
		name         string
		probeErr     error
		principal    azdext.PrincipalType
		principalID  string
		principalErr error
		hasAccess    bool
		checkErr     error
		assignErr    error
		wantChecks   int
		wantAssigns  int
		wantProbes   int
		wantWarning  bool
		wantErr      error
	}{
		{name: "externally managed custom access", wantProbes: 1},
		{name: "non-authorization probe failure", probeErr: errors.New("network failure"), wantProbes: 1, wantWarning: true},
		{name: "unrelated forbidden", probeErr: &azcore.ResponseError{
			StatusCode: 403, ErrorCode: "FirewallDenied",
		}, wantProbes: 1, wantWarning: true},
		{name: "inherited Foundry User propagates", probeErr: accessDenied(),
			principal:   azdext.PrincipalType_PRINCIPAL_TYPE_USER,
			principalID: "guest-object-id", hasAccess: true, wantChecks: 1, wantProbes: 2},
		{name: "owner without data access", probeErr: accessDenied(), principal: azdext.PrincipalType_PRINCIPAL_TYPE_USER,
			principalID: "guest-object-id", wantChecks: 1, wantAssigns: 1, wantProbes: 2},
		{name: "service principal", probeErr: accessDenied(),
			principal: azdext.PrincipalType_PRINCIPAL_TYPE_SERVICE_PRINCIPAL, principalID: "sp-object-id",
			wantChecks: 1, wantAssigns: 1, wantProbes: 2},
		{name: "principal unavailable", probeErr: accessDenied(), principalErr: errors.New("lookup failed"),
			wantProbes: 1, wantWarning: true},
		{name: "principal empty", probeErr: accessDenied(), principal: azdext.PrincipalType_PRINCIPAL_TYPE_USER,
			wantProbes: 1, wantWarning: true},
		{name: "unsupported principal", probeErr: accessDenied(), principalID: "object-id",
			wantProbes: 1, wantWarning: true},
		{name: "role visibility denied", probeErr: accessDenied(), principal: azdext.PrincipalType_PRINCIPAL_TYPE_USER,
			principalID: "object-id", checkErr: accessDenied(), wantChecks: 1, wantProbes: 1, wantWarning: true},
		{name: "grant denied", probeErr: accessDenied(), principal: azdext.PrincipalType_PRINCIPAL_TYPE_USER,
			principalID: "object-id", assignErr: accessDenied(), wantChecks: 1, wantAssigns: 1,
			wantProbes: 1, wantWarning: true},
		{name: "probe cancellation", probeErr: context.Canceled, wantProbes: 1, wantErr: context.Canceled},
		{name: "principal cancellation", probeErr: accessDenied(), principalErr: context.DeadlineExceeded,
			wantProbes: 1, wantErr: context.DeadlineExceeded},
		{name: "role check cancellation", probeErr: accessDenied(), principal: azdext.PrincipalType_PRINCIPAL_TYPE_USER,
			principalID: "object-id", checkErr: context.Canceled, wantChecks: 1, wantProbes: 1, wantErr: context.Canceled},
		{name: "grant cancellation", probeErr: accessDenied(), principal: azdext.PrincipalType_PRINCIPAL_TYPE_USER,
			principalID: "object-id", assignErr: context.Canceled, wantChecks: 1, wantAssigns: 1,
			wantProbes: 1, wantErr: context.Canceled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			account := &accessTestAccount{response: &azdext.GetCurrentPrincipalResponse{
				ObjectId: tt.principalID, PrincipalType: tt.principal,
			}, err: tt.principalErr}
			roles := &accessTestRoles{hasAccess: tt.hasAccess, checkErr: tt.checkErr, assignErr: tt.assignErr}
			probes := 0
			var warnings []string
			err := (AccessPreparation{
				Account: account, Roles: roles, Scope: accessTestScope,
				Probe: func(context.Context) error {
					probes++
					if probes == 1 {
						return tt.probeErr
					}
					require.True(t, roles.assigns == 1 || tt.hasAccess,
						"probe may only be retried after a grant or existing role")
					return nil
				},
				Warn: func(message string) { warnings = append(warnings, message) },
			}).Ensure(t.Context())
			require.ErrorIs(t, err, tt.wantErr)
			assert.Equal(t, tt.wantChecks, roles.checks)
			assert.Equal(t, tt.wantAssigns, roles.assigns)
			assert.Equal(t, tt.wantProbes, probes)
			assert.Equal(t, tt.wantWarning, len(warnings) > 0)
			if roles.assigns > 0 {
				assert.Equal(t, tt.principalID, roles.principalID)
				if tt.principal == azdext.PrincipalType_PRINCIPAL_TYPE_SERVICE_PRINCIPAL {
					assert.Equal(t, armauthorization.PrincipalTypeServicePrincipal, roles.principal)
				} else {
					assert.Equal(t, armauthorization.PrincipalTypeUser, roles.principal)
				}
			}
		})
	}
}

func TestAccessPreparationPropagation(t *testing.T) {
	for _, tt := range []struct {
		name    string
		readyAt int
	}{
		{name: "eventual readiness", readyAt: 4},
		{name: "bounded exhaustion"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			probes := 0
			roles := &accessTestRoles{}
			err := (AccessPreparation{
				Account: &accessTestAccount{response: &azdext.GetCurrentPrincipalResponse{
					ObjectId: "object-id", PrincipalType: azdext.PrincipalType_PRINCIPAL_TYPE_USER,
				}},
				Roles: roles, Scope: accessTestScope,
				Probe: func(context.Context) error {
					probes++
					if probes == tt.readyAt {
						return nil
					}
					return accessDenied()
				},
				Warn:     func(message string) { t.Errorf("unexpected warning: %s", message) },
				Attempts: 3, RetryDelay: time.Nanosecond,
			}).Ensure(t.Context())
			if tt.readyAt > 0 {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "access has not propagated")
				assert.True(t, IsProjectAccessDenied(err))
			}
			assert.Equal(t, 4, probes)
			assert.Equal(t, 1, roles.assigns)
		})
	}
}

func TestAccessPreparationProgress(t *testing.T) {
	const granting = "Assigning Foundry User to the current principal."
	const waiting = "Waiting for Foundry access to propagate."
	const ready = "Foundry access is ready."
	for _, tt := range []struct {
		name         string
		probeErrors  []error
		hasAccess    bool
		assignErr    error
		nilProgress  bool
		wantProgress []string
		wantWarning  bool
		wantErr      error
	}{
		{name: "existing access", probeErrors: []error{nil}},
		{name: "unrelated probe failure", probeErrors: []error{errors.New("network failure")}, wantWarning: true},
		{name: "inherited role propagates", probeErrors: []error{accessDenied(), nil}, hasAccess: true,
			wantProgress: []string{waiting, ready}},
		{name: "inherited role still denied after propagation wait",
			probeErrors: []error{accessDenied(), accessDenied(), accessDenied()}, hasAccess: true,
			wantProgress: []string{waiting}, wantWarning: true},
		{name: "nil callbacks on warning path", probeErrors: []error{errors.New("network failure")}, nilProgress: true},
		{name: "grant failure", probeErrors: []error{accessDenied()}, assignErr: accessDenied(),
			wantProgress: []string{granting}, wantWarning: true},
		{name: "immediate readiness", probeErrors: []error{accessDenied(), nil},
			wantProgress: []string{granting, waiting, ready}},
		{name: "eventual readiness", probeErrors: []error{accessDenied(), accessDenied(), nil},
			wantProgress: []string{granting, waiting, ready}},
		{name: "readiness unconfirmed", probeErrors: []error{accessDenied(), errors.New("network failure")},
			wantProgress: []string{granting, waiting}, wantWarning: true},
		{name: "propagation exhausted", probeErrors: []error{accessDenied(), accessDenied(), accessDenied()},
			wantProgress: []string{granting, waiting}, wantErr: accessDenied()},
		{name: "propagation canceled", probeErrors: []error{accessDenied(), context.Canceled},
			wantProgress: []string{granting, waiting}, wantErr: context.Canceled},
		{name: "nil callback after grant", probeErrors: []error{accessDenied(), nil}, nilProgress: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			roles := &accessTestRoles{hasAccess: tt.hasAccess, assignErr: tt.assignErr}
			var progress []string
			warned := false
			probes := 0
			preparation := AccessPreparation{
				Account: &accessTestAccount{response: &azdext.GetCurrentPrincipalResponse{
					ObjectId: "object-id", PrincipalType: azdext.PrincipalType_PRINCIPAL_TYPE_USER,
				}},
				Roles: roles, Scope: accessTestScope,
				Probe: func(context.Context) error {
					require.Less(t, probes, len(tt.probeErrors))
					err := tt.probeErrors[probes]
					probes++
					return err
				},
				Warn: func(string) { warned = true },
				Progress: func(message string) {
					progress = append(progress, message)
					switch message {
					case granting:
						assert.Equal(t, 0, roles.assigns, "report the attempt before assigning")
						assert.Equal(t, 1, probes)
					case waiting:
						assert.True(t, roles.assigns == 1 || tt.hasAccess)
						assert.Equal(t, 1, probes, "report the wait before probing propagation")
					case ready:
						assert.True(t, roles.assigns == 1 || tt.hasAccess)
						require.Greater(t, probes, 1)
						assert.NoError(t, tt.probeErrors[probes-1], "readiness must follow a successful probe")
					default:
						t.Errorf("unexpected progress: %s", message)
					}
				},
				Attempts: 2, RetryDelay: time.Nanosecond,
			}
			if tt.nilProgress {
				preparation.Progress = nil
				if !tt.wantWarning {
					preparation.Warn = nil
				}
			}
			err := preparation.Ensure(t.Context())
			if IsProjectAccessDenied(tt.wantErr) {
				require.ErrorContains(t, err, "access has not propagated")
				assert.True(t, IsProjectAccessDenied(err))
			} else {
				require.ErrorIs(t, err, tt.wantErr)
			}
			assert.Equal(t, len(tt.probeErrors), probes)
			assert.Equal(t, tt.wantProgress, progress)
			assert.Equal(t, tt.wantWarning, warned)
		})
	}
}

func TestAccessPreparationCancellationDuringPropagation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	probes := 0
	err := (AccessPreparation{
		Account: &accessTestAccount{response: &azdext.GetCurrentPrincipalResponse{
			ObjectId: "object-id", PrincipalType: azdext.PrincipalType_PRINCIPAL_TYPE_USER,
		}},
		Roles: &accessTestRoles{}, Scope: accessTestScope,
		Probe: func(context.Context) error {
			probes++
			if probes == 2 {
				cancel()
			}
			return accessDenied()
		},
		Warn: func(string) {},
	}).Ensure(ctx)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 2, probes)
}

func TestAccessPreparationGRPCCancellation(t *testing.T) {
	err := (AccessPreparation{
		Probe: func(context.Context) error { return status.Error(codes.Canceled, "cancelled") },
	}).Ensure(t.Context())
	require.Equal(t, codes.Canceled, status.Code(err))
}

type accessTestCredential struct{}

func (accessTestCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type accessTestTransport func(*http.Request) (*http.Response, error)

func (f accessTestTransport) Do(req *http.Request) (*http.Response, error) { return f(req) }

func newTestProjectRoles(t *testing.T, transport accessTestTransport) *projectRoles {
	t.Helper()
	client, err := armauthorization.NewRoleAssignmentsClient("sub", accessTestCredential{}, &arm.ClientOptions{
		ClientOptions: policy.ClientOptions{Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1}},
	})
	require.NoError(t, err)
	return &projectRoles{client: client}
}

func accessResponse(req *http.Request, code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Request: req, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(body))}
}

func TestProjectRolesInheritedAndEquivalentAccess(t *testing.T) {
	for _, tt := range []struct {
		name  string
		role  string
		scope string
		want  bool
	}{
		{name: "project Foundry User", role: FoundryUserRole, scope: accessTestScope, want: true},
		{name: "inherited Foundry User", role: FoundryUserRole, scope: "/subscriptions/sub", want: true},
		{name: "management group inheritance", role: FoundryUserRole,
			scope: "/providers/Microsoft.Management/managementGroups/group", want: true},
		{name: "legacy Cognitive Services User", role: projectAccessRoles[1], scope: accessTestScope, want: true},
		{name: "Foundry Project Manager", role: projectAccessRoles[2], scope: accessTestScope, want: true},
		{name: "Foundry Owner", role: projectAccessRoles[3], scope: accessTestScope, want: true},
		{name: "Owner has no data actions", role: "8e3af657-a8ff-443c-a75c-2fe8c4bcb635", scope: accessTestScope},
		{name: "Contributor has no data actions", role: "b24988ac-6180-42a0-ab88-20f7382dd24c", scope: accessTestScope},
		{name: "sibling assignment", role: FoundryUserRole, scope: accessTestScope + "-other"},
		{name: "child assignment", role: FoundryUserRole, scope: accessTestScope + "/agents/agent"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			roles := newTestProjectRoles(t, func(req *http.Request) (*http.Response, error) {
				assert.Contains(t, req.URL.Query().Get("$filter"), "assignedTo('principal')")
				return accessResponse(req, 200, `{"value":[{"properties":{"scope":"`+tt.scope+
					`","roleDefinitionId":"/providers/Microsoft.Authorization/roleDefinitions/`+tt.role+`"}}]}`), nil
			})
			hasAccess, err := roles.HasAccess(t.Context(), accessTestScope, "principal")
			require.NoError(t, err)
			assert.Equal(t, tt.want, hasAccess)
		})
	}
}

func TestProjectRolesAssignmentConflicts(t *testing.T) {
	for _, tt := range []struct {
		code    string
		wantErr bool
	}{
		{code: "RoleAssignmentExists"},
		{code: "RoleAssignmentUpdateNotPermitted", wantErr: true},
	} {
		t.Run(tt.code, func(t *testing.T) {
			var paths []string
			roles := newTestProjectRoles(t, func(req *http.Request) (*http.Response, error) {
				paths = append(paths, req.URL.Path)
				body, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				assert.Contains(t, string(body), `"principalType":"ServicePrincipal"`)
				assert.Contains(t, string(body), FoundryUserRole)
				return accessResponse(req, 409, `{"error":{"code":"`+tt.code+`","message":"conflict"}}`), nil
			})
			for range 2 {
				err := roles.Assign(
					t.Context(), accessTestScope, "principal", armauthorization.PrincipalTypeServicePrincipal)
				if tt.wantErr {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}

			}
			require.Len(t, paths, 2)
			assert.Equal(t, paths[0], paths[1], "the same project/principal must use the same assignment name")
		})
	}
}

func TestProjectRolesAssignmentRequest(t *testing.T) {
	const principalID = "11111111-2222-3333-4444-555555555555"
	for _, principalType := range []armauthorization.PrincipalType{
		armauthorization.PrincipalTypeUser, armauthorization.PrincipalTypeServicePrincipal,
	} {
		t.Run(string(principalType), func(t *testing.T) {
			calls := 0
			roles := newTestProjectRoles(t, func(request *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, http.MethodPut, request.Method)
				require.True(t, strings.HasPrefix(request.URL.Path,
					accessTestScope+"/providers/Microsoft.Authorization/roleAssignments/"))
				var parameters armauthorization.RoleAssignmentCreateParameters
				require.NoError(t, json.NewDecoder(request.Body).Decode(&parameters))
				require.NotNil(t, parameters.Properties)
				assert.Equal(t, new("/subscriptions/sub/providers/Microsoft.Authorization/roleDefinitions/"+
					FoundryUserRole), parameters.Properties.RoleDefinitionID)
				assert.Equal(t, new(principalID), parameters.Properties.PrincipalID)
				assert.Equal(t, new(principalType), parameters.Properties.PrincipalType)
				return accessResponse(request, http.StatusCreated, `{"properties":{}}`), nil
			})
			require.NoError(t, roles.Assign(t.Context(), accessTestScope, principalID, principalType))
			assert.Equal(t, 1, calls, "the preparation's role check makes a second pre-check unnecessary")
		})
	}
}
