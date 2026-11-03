// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore skiptoken

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"azure.ai.toolboxes/internal/definition"
	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/pkg/azure"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	accessTestSubscription = "11111111-1111-1111-1111-111111111111"
	accessTestPrincipal    = "22222222-2222-2222-2222-222222222222"
	accessTestScope        = "/subscriptions/" + accessTestSubscription +
		"/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj"
	accessTestEndpoint = "https://acct.services.ai.azure.com/api/projects/proj"
)

type accessEnvironmentStub struct {
	values map[string]string
	err    error
}

func (s accessEnvironmentStub) GetValues(
	context.Context, *azdext.GetEnvironmentRequest, ...grpc.CallOption,
) (*azdext.KeyValueListResponse, error) {
	response := &azdext.KeyValueListResponse{}
	for key, value := range s.values {
		response.KeyValues = append(response.KeyValues, &azdext.KeyValue{Key: key, Value: value})
	}
	return response, s.err
}

type accessAccountStub struct {
	mu                 sync.Mutex
	tenantID           string
	principalType      azdext.PrincipalType
	principalID        string
	tenantErr          error
	principalErr       error
	lookupSubscription string
	objectSubscription string
}

func (s *accessAccountStub) LookupTenant(
	_ context.Context, request *azdext.LookupTenantRequest, _ ...grpc.CallOption,
) (*azdext.LookupTenantResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lookupSubscription = request.GetSubscriptionId()
	return &azdext.LookupTenantResponse{TenantId: s.tenantID}, s.tenantErr
}

func (s *accessAccountStub) GetCurrentPrincipal(
	_ context.Context, request *azdext.GetCurrentPrincipalRequest, _ ...grpc.CallOption,
) (*azdext.GetCurrentPrincipalResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objectSubscription = request.GetSubscriptionId()
	return &azdext.GetCurrentPrincipalResponse{ObjectId: s.principalID, PrincipalType: s.principalType}, s.principalErr
}

func TestToolboxDeferredARMFailuresStillExerciseActualProbe(t *testing.T) {
	for _, boundary := range []string{"environment", "tenant", "empty tenant", "principal", "empty principal", "unsupported principal", "credential", "list", "assignment"} {
		for _, denied := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/denied=%v", boundary, denied), func(t *testing.T) {
				access := testToolboxAccess(t, func(request *http.Request) (*http.Response, error) {
					if boundary == "list" || request.Method == http.MethodPut {
						return accessHTTPResponse(request, 403, map[string]any{"error": map[string]string{"code": "AuthorizationFailed"}}), nil
					}
					return accessHTTPResponse(request, 200, map[string]any{"value": []any{}}), nil
				})
				account, ok := access.account.(*accessAccountStub)
				require.True(t, ok)
				switch boundary {
				case "environment":
					access.environment = accessEnvironmentStub{err: errors.New("unavailable")}
				case "tenant":
					account.tenantErr = errors.New("unavailable")
				case "empty tenant":
					account.tenantID = ""
				case "principal":
					account.principalErr = errors.New("unavailable")
				case "empty principal":
					account.principalID = ""
				case "unsupported principal":
					account.principalType = azdext.PrincipalType_PRINCIPAL_TYPE_UNSPECIFIED
				case "credential":
					access.newCredential = func(string) (azcore.TokenCredential, error) { return nil, errors.New("unavailable") }
				}
				access.wait = func(context.Context, time.Duration) error {
					t.Fatal("an unsuccessful ARM preparation must not trigger authorization polling")
					return nil
				}
				probes := 0
				scope, err := access.resolve(t.Context(), accessTestEndpoint)
				require.NoError(t, err)
				var progress []string
				_, err = access.ensure(t.Context(), scope, accessProbeClient{probe: func(context.Context, string) (*azure.ToolboxObject, error) {
					probes++
					if denied {
						return nil, &azcore.ResponseError{StatusCode: 403, ErrorCode: "PermissionDenied"}
					}
					return &azure.ToolboxObject{Name: "research"}, nil
				}}, "research", toolboxReadRequired, func(message string) { progress = append(progress, message) })
				assert.Equal(t, denied, err != nil)
				assert.Equal(t, 1, probes)
				assert.NotContains(t, strings.Join(progress, "\n"), "Warning")
				assert.NotContains(t, strings.Join(progress, "\n"), "propagate")
				assert.NotContains(t, strings.Join(progress, "\n"), "assigned")
			})
		}
	}
}

func TestToolboxRoleInspectionFollowsPages(t *testing.T) {
	pages := 0
	access := testToolboxAccess(t, func(request *http.Request) (*http.Response, error) {
		assert.Equal(t, http.MethodGet, request.Method, "an inherited role on a later page prevents a grant")
		pages++
		if pages == 1 {
			next, err := url.Parse(request.URL.String())
			require.NoError(t, err)
			query := next.Query()
			query.Set("$skiptoken", "next-page")
			next.RawQuery = query.Encode()
			return accessHTTPResponse(request, 200, map[string]any{"value": []any{}, "nextLink": next.String()}), nil
		}
		return accessHTTPResponse(request, 200, map[string]any{
			"value": []*armauthorization.RoleAssignment{testAccessRole(accessTestScope, foundryUserRole)},
		}), nil
	})
	scope, err := access.resolve(t.Context(), accessTestEndpoint)
	require.NoError(t, err)
	_, err = access.ensure(t.Context(), scope, newMockToolboxClient(accessTestEndpoint), "research", toolboxReadRequired, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, pages)
}

type accessCredentialStub struct{}

func (accessCredentialStub) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type accessTransport func(*http.Request) (*http.Response, error)

func (f accessTransport) Do(request *http.Request) (*http.Response, error) { return f(request) }

type accessProbeClient struct {
	toolboxClient
	probe func(context.Context, string) (*azure.ToolboxObject, error)
}

func (c accessProbeClient) GetToolbox(ctx context.Context, name string) (*azure.ToolboxObject, error) {
	return c.probe(ctx, name)
}

func accessEnvironmentValues() map[string]string {
	return map[string]string{
		"FOUNDRY_PROJECT_ENDPOINT": accessTestEndpoint,
		"AZURE_AI_PROJECT_ID":      accessTestScope,
		"AZURE_SUBSCRIPTION_ID":    accessTestSubscription,
		"AZURE_TENANT_ID":          "resource-tenant-not-for-credential",
	}
}

func testAccessRole(scope, role string) *armauthorization.RoleAssignment {
	return &armauthorization.RoleAssignment{Properties: &armauthorization.RoleAssignmentProperties{
		Scope: new(scope), RoleDefinitionID: new("/providers/Microsoft.Authorization/roleDefinitions/" + role),
	}}
}

func accessHTTPResponse(request *http.Request, code int, body any) *http.Response {
	content, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return &http.Response{
		StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(bytes.NewReader(content)), Request: request,
	}
}

func testToolboxAccess(t *testing.T, transport accessTransport) *toolboxAccess {
	t.Helper()
	client, err := armauthorization.NewRoleAssignmentsClient(accessTestSubscription, accessCredentialStub{}, &arm.ClientOptions{
		ClientOptions: policy.ClientOptions{Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1}},
	})
	require.NoError(t, err)
	return &toolboxAccess{
		environment: accessEnvironmentStub{values: accessEnvironmentValues()},
		account: &accessAccountStub{
			tenantID: "user-access-tenant", principalID: accessTestPrincipal,
			principalType: azdext.PrincipalType_PRINCIPAL_TYPE_USER,
		},
		newCredential: func(tenant string) (azcore.TokenCredential, error) {
			assert.Equal(t, "user-access-tenant", tenant)
			return accessCredentialStub{}, nil
		},
		roleClient: client,
		wait:       func(context.Context, time.Duration) error { return nil },
		attempts:   3,
	}
}

func TestToolboxScopeBinding(t *testing.T) {
	for _, tt := range []struct {
		name     string
		endpoint string
		change   func(map[string]string)
		want     bool
	}{
		{"matching explicit endpoint", accessTestEndpoint, nil, true},
		{"normalized endpoint", accessTestEndpoint + "/", nil, true},
		{"legacy whitespace fallback", accessTestEndpoint, func(v map[string]string) {
			v["FOUNDRY_PROJECT_ENDPOINT"] = " \t"
			v["AZURE_AI_PROJECT_ENDPOINT"] = accessTestEndpoint
		}, true},
		{"unrelated explicit", "https://other.services.ai.azure.com/api/projects/proj", nil, false},
		{"unrelated project", "https://acct.services.ai.azure.com/api/projects/other", nil, false},
		{"custom subdomain", "https://custom.services.ai.azure.com/api/projects/proj", func(v map[string]string) {
			v["FOUNDRY_PROJECT_ENDPOINT"] = "https://custom.services.ai.azure.com/api/projects/proj"
		}, false},
		{"subscription mismatch", accessTestEndpoint, func(v map[string]string) { v["AZURE_SUBSCRIPTION_ID"] = "other" }, false},
		{"missing subscription", accessTestEndpoint, func(v map[string]string) { delete(v, "AZURE_SUBSCRIPTION_ID") }, false},
		{"missing project ID", accessTestEndpoint, func(v map[string]string) { delete(v, "AZURE_AI_PROJECT_ID") }, false},
		{"subscription scope", accessTestEndpoint, func(v map[string]string) {
			v["AZURE_AI_PROJECT_ID"] = "/subscriptions/" + accessTestSubscription
		}, false},
		{"resource group scope", accessTestEndpoint, func(v map[string]string) {
			v["AZURE_AI_PROJECT_ID"] = "/subscriptions/" + accessTestSubscription + "/resourceGroups/rg"
		}, false},
		{"account scope", accessTestEndpoint, func(v map[string]string) {
			v["AZURE_AI_PROJECT_ID"] = strings.TrimSuffix(accessTestScope, "/projects/proj")
		}, false},
		{"child scope", accessTestEndpoint, func(v map[string]string) { v["AZURE_AI_PROJECT_ID"] += "/connections/c" }, false},
		{"wrong provider", accessTestEndpoint, func(v map[string]string) {
			v["AZURE_AI_PROJECT_ID"] = strings.ReplaceAll(accessTestScope, "Microsoft.CognitiveServices", "Microsoft.Other")
		}, false},
		{"different ARM project", accessTestEndpoint, func(v map[string]string) {
			v["AZURE_AI_PROJECT_ID"] = strings.TrimSuffix(accessTestScope, "proj") + "other"
		}, false},
		{"credential URL", "https://user:secret@acct.services.ai.azure.com/api/projects/proj?sig=secret#secret", nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			values := accessEnvironmentValues()
			if tt.change != nil {
				tt.change(values)
			}
			scope := toolboxScopeFromEnvironment(tt.endpoint, values)
			assert.Equal(t, tt.want, scope != nil)
		})
	}
}

func TestToolboxRolesARMBeforeProbe(t *testing.T) {
	for _, tt := range []struct {
		name string
		role string
		at   string
		want bool
	}{
		{"subscription inherited", foundryUserRole, "/subscriptions/" + accessTestSubscription, true},
		{"group inherited", foundryUserRole, "/subscriptions/" + accessTestSubscription + "/resourceGroups/rg", true},
		{"account inherited", foundryUserRole, strings.TrimSuffix(accessTestScope, "/projects/proj"), true},
		{"project direct", foundryUserRole, accessTestScope, true},
		{"management group inherited", foundryUserRole, "/providers/Microsoft.Management/managementGroups/parent", true},
		{"legacy cognitive user", sufficientToolboxRoles[1], accessTestScope, true},
		{"project manager", sufficientToolboxRoles[2], accessTestScope, true},
		{"foundry owner", sufficientToolboxRoles[3], accessTestScope, true},
		{"Azure Owner is insufficient", "8e3af657-a8ff-443c-a75c-2fe8c4bcb635", accessTestScope, false},
		{"Azure Contributor is insufficient", "b24988ac-6180-42a0-ab88-20f7382dd24c", accessTestScope, false},
		{"child role is insufficient", foundryUserRole, accessTestScope + "/connections/child", false},
		{"unrelated role is insufficient", foundryUserRole, strings.TrimSuffix(accessTestScope, "proj") + "other", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			events := []string{}
			access := testToolboxAccess(t, func(request *http.Request) (*http.Response, error) {
				events = append(events, request.Method)
				if request.Method == http.MethodGet {
					assert.Contains(t, request.URL.Query().Get("$filter"), accessTestPrincipal)
					assert.Equal(t, accessTestScope+"/providers/Microsoft.Authorization/roleAssignments", request.URL.Path)
					return accessHTTPResponse(request, 200, map[string]any{
						"value": []*armauthorization.RoleAssignment{testAccessRole(tt.at, tt.role)},
					}), nil
				}
				assert.Equal(t, http.MethodPut, request.Method)
				return accessHTTPResponse(request, 201, map[string]any{}), nil
			})
			access.wait = func(_ context.Context, duration time.Duration) error {
				assert.Equal(t, 15*time.Second, duration)
				events = append(events, "settle")
				return nil
			}
			scope, err := access.resolve(t.Context(), accessTestEndpoint)
			require.NoError(t, err)
			_, err = access.ensure(t.Context(), scope, accessProbeClient{probe: func(context.Context, string) (*azure.ToolboxObject, error) {
				events = append(events, "probe")
				return nil, notFoundResponseError("toolbox not found")
			}}, "research", toolboxReadRequired, nil)
			require.NoError(t, err)
			if tt.want {
				assert.Equal(t, []string{"GET", "probe"}, events)
			} else {
				assert.Equal(t, []string{"GET", "PUT", "settle", "probe"}, events)
			}
		})
	}
	conditional := testAccessRole(accessTestScope, foundryUserRole)
	conditional.Properties.Condition = new("some condition")
	assert.False(t, toolboxRoleSufficient(conditional, accessTestScope))
	assert.False(t, toolboxRoleSufficient(nil, accessTestScope))
}

func TestToolboxAssignmentIdentityAndConflicts(t *testing.T) {
	for _, principalType := range []azdext.PrincipalType{
		azdext.PrincipalType_PRINCIPAL_TYPE_USER, azdext.PrincipalType_PRINCIPAL_TYPE_SERVICE_PRINCIPAL,
	} {
		for _, conflict := range []string{"", "RoleAssignmentExists", "OtherConflict"} {
			t.Run(principalType.String()+conflict, func(t *testing.T) {
				names := []string{}
				access := testToolboxAccess(t, func(request *http.Request) (*http.Response, error) {
					if request.Method == http.MethodGet {
						return accessHTTPResponse(request, 200, map[string]any{"value": []any{}}), nil
					}
					scope, name, found := strings.Cut(request.URL.Path, "/providers/Microsoft.Authorization/roleAssignments/")
					require.True(t, found)
					assert.Equal(t, accessTestScope, scope)
					_, err := uuid.Parse(name)
					require.NoError(t, err)
					names = append(names, name)
					var body armauthorization.RoleAssignmentCreateParameters
					require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
					require.NotNil(t, body.Properties)
					assert.Equal(t, accessTestPrincipal, *body.Properties.PrincipalID)
					assert.Equal(t, "/subscriptions/"+accessTestSubscription+
						"/providers/Microsoft.Authorization/roleDefinitions/"+foundryUserRole, *body.Properties.RoleDefinitionID)
					expectedType := armauthorization.PrincipalTypeUser
					if principalType == azdext.PrincipalType_PRINCIPAL_TYPE_SERVICE_PRINCIPAL {
						expectedType = armauthorization.PrincipalTypeServicePrincipal
					}
					assert.Equal(t, expectedType, *body.Properties.PrincipalType)
					if conflict != "" {
						return accessHTTPResponse(request, 409, map[string]any{"error": map[string]string{"code": conflict}}), nil
					}
					return accessHTTPResponse(request, 201, map[string]any{}), nil
				})
				account, ok := access.account.(*accessAccountStub)
				require.True(t, ok)
				account.principalType = principalType
				scope, err := access.resolve(t.Context(), accessTestEndpoint)
				require.NoError(t, err)
				assert.Equal(t, "user-access-tenant", scope.tenantID)
				assert.Equal(t, accessTestSubscription, account.lookupSubscription)
				for range 2 {
					confirmed, assigned, principalID, _, err := access.prepareRole(t.Context(), scope, nil)
					assert.Equal(t, accessTestPrincipal, principalID)
					assert.Equal(t, accessTestSubscription, account.objectSubscription)
					if conflict == "OtherConflict" {
						require.Error(t, err)
						assert.False(t, confirmed)
						assert.False(t, assigned)
					} else {
						require.NoError(t, err)
						assert.True(t, confirmed)
						assert.True(t, assigned)
					}
				}
				require.Len(t, names, 2)
				assert.Equal(t, names[0], names[1])
			})
		}
	}
}

func TestToolboxAccessReadiness(t *testing.T) {
	denied := &azcore.ResponseError{StatusCode: 403, ErrorCode: "PermissionDenied"}
	projectMissing := notFoundResponseError("Project not found")
	toolboxMissing := notFoundResponseError("Toolbox not found")
	policyDenied := &azcore.ResponseError{StatusCode: 403, ErrorCode: "PolicyDenied"}
	for _, tt := range []struct {
		name       string
		armDenied  bool
		existing   bool
		noAccount  bool
		probes     []error
		wantErr    bool
		wantProbes int
		wantWaits  []time.Duration
	}{
		{"new grant settles before first probe", false, false, false, []error{toolboxMissing}, false, 1, []time.Duration{15 * time.Second}},
		{"new grant propagation", false, false, false, []error{denied, nil}, false, 2, []time.Duration{15 * time.Second, 5 * time.Second}},
		{"new grant exhausted", false, false, false, []error{denied}, true, 3, []time.Duration{15 * time.Second, 5 * time.Second, 5 * time.Second}},
		{"existing role propagation", false, true, false, []error{denied, nil}, false, 2, []time.Duration{5 * time.Second}},
		{"existing role exhausted", false, true, false, []error{denied}, true, 3, []time.Duration{5 * time.Second, 5 * time.Second}},
		{"ARM denied custom access succeeds", true, false, false, []error{nil}, false, 1, nil},
		{"ARM denied access fails without propagation polling", true, false, false, []error{denied}, true, 1, nil},
		{"no account denial", false, false, true, []error{denied}, true, 1, nil},
		{"project sync independent of ARM", true, false, false, []error{projectMissing, toolboxMissing}, false, 2, []time.Duration{5 * time.Second}},
		{"no account project sync", false, false, true, []error{projectMissing, nil}, false, 2, []time.Duration{5 * time.Second}},
		{"project sync exhausted", true, false, false, []error{projectMissing}, true, 3, []time.Duration{5 * time.Second, 5 * time.Second}},
		{"project sync does not unlock auth retries", true, false, false, []error{projectMissing, denied}, true, 2, []time.Duration{5 * time.Second}},
		{"policy failure authoritative", false, true, false, []error{policyDenied}, true, 1, nil},
		{"network failure authoritative", false, true, false, []error{errors.New("network failed")}, true, 1, nil},
		{"server failure authoritative", false, true, false, []error{&azcore.ResponseError{StatusCode: 500}}, true, 1, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			access := testToolboxAccess(t, func(request *http.Request) (*http.Response, error) {
				if tt.armDenied {
					return accessHTTPResponse(request, 403, map[string]any{"error": map[string]string{"code": "AuthorizationFailed"}}), nil
				}
				if request.Method == http.MethodGet {
					roles := []*armauthorization.RoleAssignment{}
					if tt.existing {
						roles = append(roles, testAccessRole(accessTestScope, foundryUserRole))
					}
					return accessHTTPResponse(request, 200, map[string]any{"value": roles}), nil
				}
				return accessHTTPResponse(request, 201, map[string]any{}), nil
			})
			if tt.noAccount {
				access.account = nil
			}
			var waits []time.Duration
			access.wait = func(_ context.Context, duration time.Duration) error { waits = append(waits, duration); return nil }
			probes := 0
			client := accessProbeClient{probe: func(ctx context.Context, name string) (*azure.ToolboxObject, error) {
				assert.Equal(t, "research", name)
				deadline, ok := ctx.Deadline()
				assert.True(t, ok)
				assert.LessOrEqual(t, time.Until(deadline), 5*time.Minute)
				err := tt.probes[min(probes, len(tt.probes)-1)]
				probes++
				if err != nil {
					return nil, err
				}
				return &azure.ToolboxObject{Name: name}, nil
			}}
			scope, err := access.resolve(t.Context(), accessTestEndpoint)
			require.NoError(t, err)
			messages := []string{}
			_, err = access.ensure(t.Context(), scope, client, "research", toolboxReadRequired, func(message string) { messages = append(messages, message) })
			assert.Equal(t, tt.wantErr, err != nil)
			assert.Equal(t, tt.wantProbes, probes)
			assert.Equal(t, tt.wantWaits, waits)
			if tt.armDenied || tt.noAccount {
				assert.NotContains(t, strings.Join(messages, "\n"), "propagate")
				assert.NotContains(t, strings.Join(messages, "\n"), "assigned")
			}
			assert.NotContains(t, strings.Join(messages, "\n"), "Warning")
		})
	}
}

func TestToolboxOptOutAndUnverifiedContext(t *testing.T) {
	for _, tt := range []struct {
		name       string
		envValue   string
		process    string
		unverified bool
		wantARM    bool
	}{
		{"env true", "true", "", false, false},
		{"env one", "1", "", false, false},
		{"process true", "", "true", false, false},
		{"env false overrides process", "false", "true", false, true},
		{"invalid nonempty overrides process", "not-a-bool", "true", false, true},
		{"whitespace is not ParseBool true", " true ", "true", false, true},
		{"missing binding", "", "", true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AZD_AGENT_SKIP_ROLE_ASSIGNMENTS", tt.process)
			armCalls := 0
			access := testToolboxAccess(t, func(request *http.Request) (*http.Response, error) {
				armCalls++
				return accessHTTPResponse(request, 200, map[string]any{
					"value": []*armauthorization.RoleAssignment{testAccessRole(accessTestScope, foundryUserRole)},
				}), nil
			})
			values := accessEnvironmentValues()
			values["AZD_AGENT_SKIP_ROLE_ASSIGNMENTS"] = tt.envValue
			if tt.unverified {
				delete(values, "AZURE_AI_PROJECT_ID")
			}
			access.environment = accessEnvironmentStub{values: values}
			scope, err := access.resolve(t.Context(), accessTestEndpoint)
			require.NoError(t, err)
			probes := 0
			_, err = access.ensure(t.Context(), scope, accessProbeClient{probe: func(context.Context, string) (*azure.ToolboxObject, error) {
				probes++
				return nil, notFoundResponseError("toolbox not found")
			}}, "research", toolboxReadRequired, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.wantARM, armCalls > 0)
			assert.Equal(t, 1, probes)
		})
	}
}

func TestToolboxCancellationPrecedesNotFound(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, status.Error(codes.Canceled, "cancelled"), status.Error(codes.DeadlineExceeded, "deadline")} {
		t.Run(cause.Error(), func(t *testing.T) {
			joined := errors.Join(notFoundResponseError("Project not found"), cause)
			_, err := (*toolboxAccess)(nil).ensure(t.Context(), nil, accessProbeClient{probe: func(context.Context, string) (*azure.ToolboxObject, error) {
				return nil, joined
			}}, "research", toolboxReadRequired, nil)
			require.ErrorIs(t, err, cause)
			err = toolboxWriteError(t.Context(), joined, exterrors.OpGetToolbox, nil, "", "")
			require.ErrorIs(t, err, cause)
		})
	}
	for _, boundary := range []string{"environment", "tenant", "principal", "role list", "role assignment", "settle", "probe deadline"} {
		t.Run(boundary, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			access := testToolboxAccess(t, func(request *http.Request) (*http.Response, error) {
				if boundary == "role list" || (boundary == "role assignment" && request.Method == http.MethodPut) {
					cancel()
					return nil, context.Canceled
				}
				if request.Method == http.MethodGet {
					return accessHTTPResponse(request, 200, map[string]any{"value": []any{}}), nil
				}
				return accessHTTPResponse(request, 201, map[string]any{}), nil
			})
			account, ok := access.account.(*accessAccountStub)
			require.True(t, ok)
			switch boundary {
			case "environment":
				access.environment = accessEnvironmentStub{err: context.Canceled}
			case "tenant":
				account.tenantErr = context.Canceled
			case "principal":
				account.principalErr = context.Canceled
			case "settle":
				access.wait = func(ctx context.Context, _ time.Duration) error {
					cancel()
					return waitToolboxAccess(ctx, time.Hour)
				}
			case "probe deadline":
				access.account = nil
			}
			scope, err := access.resolve(ctx, accessTestEndpoint)
			if err == nil {
				_, err = access.ensure(ctx, scope, accessProbeClient{probe: func(context.Context, string) (*azure.ToolboxObject, error) {
					if boundary == "probe deadline" {
						return nil, context.DeadlineExceeded
					}
					t.Fatal("probe must not run after cancellation")
					return nil, nil
				}}, "research", toolboxReadRequired, nil)
			}
			if boundary == "probe deadline" {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			} else {
				require.ErrorIs(t, err, context.Canceled)
			}
		})
	}
}

func TestToolboxReadinessBudgetRetainsServiceRemediation(t *testing.T) {
	access := testToolboxAccess(t, func(request *http.Request) (*http.Response, error) {
		return accessHTTPResponse(request, 200, map[string]any{
			"value": []*armauthorization.RoleAssignment{testAccessRole(accessTestScope, foundryUserRole)},
		}), nil
	})
	access.timeout = time.Second
	access.wait = waitToolboxAccess
	scope, err := access.resolve(t.Context(), accessTestEndpoint)
	require.NoError(t, err)
	_, err = access.ensure(t.Context(), scope, accessProbeClient{probe: func(context.Context, string) (*azure.ToolboxObject, error) {
		return nil, &azcore.ResponseError{StatusCode: 403, ErrorCode: "PermissionDenied"}
	}}, "research", toolboxReadRequired, nil)
	service, ok := errors.AsType[*azdext.ServiceError](err)
	require.True(t, ok, "the internal budget should retain the Foundry failure: %v", err)
	assert.Equal(t, 403, service.StatusCode)
	assert.Contains(t, azdext.WrapError(err).GetSuggestion(), "az role assignment create")

	parent, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
	defer cancel()
	_, err = access.ensure(parent, scope, accessProbeClient{probe: func(context.Context, string) (*azure.ToolboxObject, error) {
		return nil, &azcore.ResponseError{StatusCode: 403, ErrorCode: "PermissionDenied"}
	}}, "research", toolboxReadRequired, nil)
	require.ErrorIs(t, err, context.DeadlineExceeded, "caller deadlines remain authoritative")
}

func TestToolboxJoinedConflictDoesNotHideCancellation(t *testing.T) {
	access := testToolboxAccess(t, func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodGet {
			return accessHTTPResponse(request, 200, map[string]any{"value": []any{}}), nil
		}
		return nil, errors.Join(&azcore.ResponseError{StatusCode: 409, ErrorCode: "RoleAssignmentExists"}, context.Canceled)
	})
	scope, err := access.resolve(t.Context(), accessTestEndpoint)
	require.NoError(t, err)
	confirmed, assigned, _, _, err := access.prepareRole(t.Context(), scope, nil)
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, confirmed)
	assert.False(t, assigned)
}

func TestToolboxStructuredRemediationDoesNotDiscloseURL(t *testing.T) {
	secretURL, err := url.Parse("https://username:password@acct.services.ai.azure.com/api/projects/proj?sig=secret#fragment")
	require.NoError(t, err)
	responseErr := &azcore.ResponseError{
		StatusCode: 403, ErrorCode: "PermissionDenied",
		RawResponse: &http.Response{
			StatusCode: 403, Request: &http.Request{URL: secretURL, Method: http.MethodGet},
			Body: io.NopCloser(strings.NewReader("sensitive service payload")),
		},
	}
	scope := &toolboxAccessScope{resource: toolboxProjectResource(accessTestScope)}
	converted := toolboxWriteError(t.Context(), responseErr, exterrors.OpGetToolbox, scope, accessTestPrincipal, armauthorization.PrincipalTypeUser)
	structured, ok := errors.AsType[*azdext.ServiceError](converted)
	require.True(t, ok)
	assert.Equal(t, 403, structured.StatusCode)
	assert.Contains(t, structured.Suggestion, "az role assignment create")
	assert.Contains(t, structured.Suggestion, "--scope "+fmt.Sprintf("%q", accessTestScope))
	assert.Contains(t, structured.Suggestion, accessTestPrincipal)
	wire := azdext.WrapError(converted)
	assert.Equal(t, structured.Message, wire.GetMessage())
	assert.Equal(t, structured.Suggestion, wire.GetSuggestion())
	encoded, err := json.Marshal(wire)
	require.NoError(t, err)
	for _, secret := range []string{"username", "password", "sig=", "secret", "fragment", "sensitive service payload"} {
		assert.NotContains(t, string(encoded), secret)
	}
}

func TestToolboxWriteAuthenticationGuidance(t *testing.T) {
	for _, cause := range []error{new(azidentity.AuthenticationFailedError), new(azidentity.AuthenticationRequiredError)} {
		err := toolboxWriteError(t.Context(), cause, exterrors.OpGetToolbox, nil, "", "")
		local := requireLocalError(t, err, exterrors.CodeAuthFailed)
		assert.Equal(t, azdext.LocalErrorCategoryAuth, local.Category)
		assert.Contains(t, azdext.WrapError(err).GetSuggestion(), "azd auth login")
	}
}

func TestToolboxWriteEndpointDoesNotDiscloseMalformedURL(t *testing.T) {
	_, err := resolveToolboxWriteEndpoint(t.Context(),
		"https://username:password@acct.services.ai.azure.com/api/projects/proj%zz?sig=secret#fragment")
	require.Error(t, err)
	wire := azdext.WrapError(err)
	for _, secret := range []string{"username", "password", "sig=", "secret", "fragment"} {
		assert.NotContains(t, wire.GetMessage(), secret)
		assert.NotContains(t, wire.GetSuggestion(), secret)
	}
}

func TestToolboxStandaloneJSONUsesSeparateDiagnostics(t *testing.T) {
	stubToolboxEndpointEnv(t)
	events := []string{}
	access := testToolboxAccess(t, func(request *http.Request) (*http.Response, error) {
		events = append(events, request.Method)
		if request.Method == http.MethodGet {
			return accessHTTPResponse(request, 200, map[string]any{"value": []any{}}), nil
		}
		return accessHTTPResponse(request, 201, map[string]any{}), nil
	})
	scope, err := access.resolve(t.Context(), accessTestEndpoint)
	require.NoError(t, err)
	var stdout, stderr bytes.Buffer
	mock := newMockToolboxClient(accessTestEndpoint)
	probes := 0
	client := accessProbeClient{toolboxClient: mock, probe: func(context.Context, string) (*azure.ToolboxObject, error) {
		events = append(events, "probe")
		probes++
		return nil, notFoundResponseError("toolbox not found")
	}}
	err = createToolboxFromInput(t.Context(), client, newStubConnectionResolver(), accessTestEndpoint, "research",
		&definition.Definition{Tools: []map[string]any{{"type": "web_search"}}}, toolboxFlags{output: "json"},
		access, scope, &stdout, func(message string) { fmt.Fprintln(&stderr, message) })
	require.NoError(t, err)
	assert.Equal(t, []string{"GET", "PUT", "probe"}, events)
	assert.Equal(t, 1, probes, "the readiness read also performs the create existence check")
	assert.Contains(t, stderr.String(), "Assigning Foundry User")
	assert.Contains(t, stderr.String(), "ready")
	assert.NotContains(t, stdout.String(), "Assigning")
	var payload map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &payload))
	assert.Equal(t, "research", payload["toolbox"])
	require.Len(t, mock.createVersionCalls, 1)
}
