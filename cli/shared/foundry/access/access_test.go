// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package access

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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/azure/azure-dev/cli/shared/foundry/ferrors"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testSubscription = "11111111-1111-1111-1111-111111111111"
	testPrincipal    = "22222222-2222-2222-2222-222222222222"
	testScope        = "/subscriptions/" + testSubscription +
		"/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj"
	testEndpoint = "https://acct.services.ai.azure.com/api/projects/proj"
)

type environmentStub struct {
	values map[string]string
	err    error
}

func (s environmentStub) Values(context.Context) (map[string]string, error) {
	return s.values, s.err
}

type accountStub struct {
	mu                 sync.Mutex
	tenantID           string
	principalType      armauthorization.PrincipalType
	principalID        string
	tenantErr          error
	principalErr       error
	lookupSubscription string
	objectSubscription string
}

func (s *accountStub) LookupTenant(_ context.Context, subscriptionID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lookupSubscription = subscriptionID
	return s.tenantID, s.tenantErr
}

func (s *accountStub) CurrentPrincipal(_ context.Context, subscriptionID string) (Principal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objectSubscription = subscriptionID
	return Principal{ObjectID: s.principalID, Type: s.principalType}, s.principalErr
}

type credentialStub struct{}

func (credentialStub) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) Do(request *http.Request) (*http.Response, error) { return f(request) }

type testResource struct {
	Name string
}

func defaultEnvironmentValues() map[string]string {
	return map[string]string{
		"FOUNDRY_PROJECT_ENDPOINT": testEndpoint,
		"AZURE_AI_PROJECT_ID":      testScope,
		"AZURE_SUBSCRIPTION_ID":    testSubscription,
		"AZURE_TENANT_ID":          "resource-tenant-not-for-credential",
	}
}

func testRoleAssignment(scope, role string) *armauthorization.RoleAssignment {
	return &armauthorization.RoleAssignment{Properties: &armauthorization.RoleAssignmentProperties{
		Scope:            new(scope),
		RoleDefinitionID: new("/providers/Microsoft.Authorization/roleDefinitions/" + role),
	}}
}

func httpResponse(request *http.Request, code int, body any) *http.Response {
	content, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return &http.Response{
		StatusCode: code,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(content)),
		Request:    request,
	}
}

func notFoundResponseError(message string) error {
	reqURL, _ := url.Parse(testEndpoint)
	raw := httpResponse(&http.Request{Method: http.MethodGet, URL: reqURL}, http.StatusNotFound, map[string]any{
		"error": map[string]string{"code": "NotFound", "message": message},
	})
	return &azcore.ResponseError{
		StatusCode:  http.StatusNotFound,
		ErrorCode:   "NotFound",
		RawResponse: raw,
	}
}

func newTestAccessClient(t *testing.T, transport transportFunc) *Client {
	t.Helper()
	roleClient, err := armauthorization.NewRoleAssignmentsClient(
		testSubscription,
		credentialStub{},
		&arm.ClientOptions{
			ClientOptions: policy.ClientOptions{
				Transport: transport,
				Retry:     policy.RetryOptions{MaxRetries: -1},
			},
		},
	)
	require.NoError(t, err)
	return &Client{
		Environment: environmentStub{values: defaultEnvironmentValues()},
		Account: &accountStub{
			tenantID:      "user-access-tenant",
			principalID:   testPrincipal,
			principalType: armauthorization.PrincipalTypeUser,
		},
		NewCredential: func(tenant string) (azcore.TokenCredential, error) {
			assert.Equal(t, "user-access-tenant", tenant)
			return credentialStub{}, nil
		},
		RoleClient:       roleClient,
		Wait:             func(context.Context, time.Duration) error { return nil },
		Attempts:         3,
		ResourceNoun:     "toolbox",
		DefaultOperation: "get_toolbox",
	}
}

func TestDeferredARMFailuresStillExerciseActualProbe(t *testing.T) {
	boundaries := []string{
		"environment", "tenant", "empty tenant", "principal",
		"empty principal", "unsupported principal", "credential", "list", "assignment",
	}
	for _, boundary := range boundaries {
		for _, denied := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/denied=%v", boundary, denied), func(t *testing.T) {
				client := newTestAccessClient(t, func(request *http.Request) (*http.Response, error) {
					if boundary == "list" || request.Method == http.MethodPut {
						return httpResponse(request, 403, map[string]any{
							"error": map[string]string{"code": "AuthorizationFailed"},
						}), nil
					}
					return httpResponse(request, 200, map[string]any{"value": []any{}}), nil
				})
				account, ok := client.Account.(*accountStub)
				require.True(t, ok)
				switch boundary {
				case "environment":
					client.Environment = environmentStub{err: errors.New("unavailable")}
				case "tenant":
					account.tenantErr = errors.New("unavailable")
				case "empty tenant":
					account.tenantID = ""
				case "principal":
					account.principalErr = errors.New("unavailable")
				case "empty principal":
					account.principalID = ""
				case "unsupported principal":
					account.principalType = armauthorization.PrincipalType("Unsupported")
				case "credential":
					client.NewCredential = func(string) (azcore.TokenCredential, error) {
						return nil, errors.New("unavailable")
					}
				}
				client.Wait = func(context.Context, time.Duration) error {
					t.Fatal("an unsuccessful ARM preparation must not trigger authorization polling")
					return nil
				}
				probes := 0
				scope, err := client.Resolve(t.Context(), testEndpoint)
				require.NoError(t, err)
				var progress []string
				_, err = Ensure(
					t.Context(),
					client,
					scope,
					ReadRequired,
					func(message string) { progress = append(progress, message) },
					func(context.Context) (*testResource, error) {
						probes++
						if denied {
							return nil, &azcore.ResponseError{StatusCode: 403, ErrorCode: "PermissionDenied"}
						}
						return &testResource{Name: "research"}, nil
					},
				)
				assert.Equal(t, denied, err != nil)
				assert.Equal(t, 1, probes)
				assert.NotContains(t, strings.Join(progress, "\n"), "Warning")
				assert.NotContains(t, strings.Join(progress, "\n"), "propagate")
				assert.NotContains(t, strings.Join(progress, "\n"), "assigned")
			})
		}
	}
}

func TestRoleInspectionFollowsPages(t *testing.T) {
	pages := 0
	client := newTestAccessClient(t, func(request *http.Request) (*http.Response, error) {
		assert.Equal(t, http.MethodGet, request.Method, "an inherited role on a later page prevents a grant")
		pages++
		if pages == 1 {
			next, err := url.Parse(request.URL.String())
			require.NoError(t, err)
			query := next.Query()
			query.Set("$skiptoken", "next-page")
			next.RawQuery = query.Encode()
			return httpResponse(request, 200, map[string]any{"value": []any{}, "nextLink": next.String()}), nil
		}
		return httpResponse(request, 200, map[string]any{
			"value": []*armauthorization.RoleAssignment{testRoleAssignment(testScope, FoundryUserRoleID)},
		}), nil
	})
	scope, err := client.Resolve(t.Context(), testEndpoint)
	require.NoError(t, err)
	got, err := Ensure(t.Context(), client, scope, ReadRequired, nil, func(context.Context) (*testResource, error) {
		return &testResource{Name: "research"}, nil
	})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, 2, pages)
}

func TestScopeBinding(t *testing.T) {
	for _, tt := range []struct {
		name     string
		endpoint string
		change   func(map[string]string)
		want     bool
	}{
		{"matching explicit endpoint", testEndpoint, nil, true},
		{"normalized endpoint", testEndpoint + "/", nil, true},
		{"legacy whitespace fallback", testEndpoint, func(v map[string]string) {
			v["FOUNDRY_PROJECT_ENDPOINT"] = " \t"
			v["AZURE_AI_PROJECT_ENDPOINT"] = testEndpoint
		}, true},
		{"unrelated explicit", "https://other.services.ai.azure.com/api/projects/proj", nil, false},
		{"unrelated project", "https://acct.services.ai.azure.com/api/projects/other", nil, false},
		{"custom subdomain", "https://custom.services.ai.azure.com/api/projects/proj", func(v map[string]string) {
			v["FOUNDRY_PROJECT_ENDPOINT"] = "https://custom.services.ai.azure.com/api/projects/proj"
		}, false},
		{"subscription mismatch", testEndpoint, func(v map[string]string) { v["AZURE_SUBSCRIPTION_ID"] = "other" }, false},
		{"missing subscription", testEndpoint, func(v map[string]string) { delete(v, "AZURE_SUBSCRIPTION_ID") }, false},
		{"missing project ID", testEndpoint, func(v map[string]string) { delete(v, "AZURE_AI_PROJECT_ID") }, false},
		{"subscription scope", testEndpoint, func(v map[string]string) {
			v["AZURE_AI_PROJECT_ID"] = "/subscriptions/" + testSubscription
		}, false},
		{"resource group scope", testEndpoint, func(v map[string]string) {
			v["AZURE_AI_PROJECT_ID"] = "/subscriptions/" + testSubscription + "/resourceGroups/rg"
		}, false},
		{"account scope", testEndpoint, func(v map[string]string) {
			v["AZURE_AI_PROJECT_ID"] = strings.TrimSuffix(testScope, "/projects/proj")
		}, false},
		{"child scope", testEndpoint, func(v map[string]string) { v["AZURE_AI_PROJECT_ID"] += "/connections/c" }, false},
		{"wrong provider", testEndpoint, func(v map[string]string) {
			v["AZURE_AI_PROJECT_ID"] = strings.ReplaceAll(testScope, "Microsoft.CognitiveServices", "Microsoft.Other")
		}, false},
		{"different ARM project", testEndpoint, func(v map[string]string) {
			v["AZURE_AI_PROJECT_ID"] = strings.TrimSuffix(testScope, "proj") + "other"
		}, false},
		{"credential URL", "https://user:secret@acct.services.ai.azure.com/api/projects/proj?sig=secret#secret", nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			values := defaultEnvironmentValues()
			if tt.change != nil {
				tt.change(values)
			}
			scope := ScopeFromEnvironment(tt.endpoint, values)
			assert.Equal(t, tt.want, scope != nil)
		})
	}
}

func TestRolesARMBeforeProbe(t *testing.T) {
	for _, tt := range []struct {
		name string
		role string
		at   string
		want bool
	}{
		{"subscription inherited", FoundryUserRoleID, "/subscriptions/" + testSubscription, true},
		{"group inherited", FoundryUserRoleID, "/subscriptions/" + testSubscription + "/resourceGroups/rg", true},
		{"account inherited", FoundryUserRoleID, strings.TrimSuffix(testScope, "/projects/proj"), true},
		{"project direct", FoundryUserRoleID, testScope, true},
		{"management group inherited", FoundryUserRoleID, "/providers/Microsoft.Management/managementGroups/parent", true},
		{"legacy cognitive user", SufficientDataPlaneRoles[1], testScope, true},
		{"project manager", SufficientDataPlaneRoles[2], testScope, true},
		{"foundry owner", SufficientDataPlaneRoles[3], testScope, true},
		{"Azure Owner is insufficient", "8e3af657-a8ff-443c-a75c-2fe8c4bcb635", testScope, false},
		{"Azure Contributor is insufficient", "b24988ac-6180-42a0-ab88-20f7382dd24c", testScope, false},
		{"child role is insufficient", FoundryUserRoleID, testScope + "/connections/child", false},
		{"unrelated role is insufficient", FoundryUserRoleID, strings.TrimSuffix(testScope, "proj") + "other", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			events := []string{}
			client := newTestAccessClient(t, func(request *http.Request) (*http.Response, error) {
				events = append(events, request.Method)
				if request.Method == http.MethodGet {
					assert.Contains(t, request.URL.Query().Get("$filter"), testPrincipal)
					assert.Equal(t, testScope+"/providers/Microsoft.Authorization/roleAssignments", request.URL.Path)
					return httpResponse(request, 200, map[string]any{
						"value": []*armauthorization.RoleAssignment{testRoleAssignment(tt.at, tt.role)},
					}), nil
				}
				assert.Equal(t, http.MethodPut, request.Method)
				return httpResponse(request, 201, map[string]any{}), nil
			})
			client.Wait = func(_ context.Context, duration time.Duration) error {
				assert.Equal(t, 15*time.Second, duration)
				events = append(events, "settle")
				return nil
			}
			scope, err := client.Resolve(t.Context(), testEndpoint)
			require.NoError(t, err)
			_, err = Ensure(t.Context(), client, scope, ReadRequired, nil, func(context.Context) (*testResource, error) {
				events = append(events, "probe")
				return nil, notFoundResponseError("toolbox not found")
			})
			require.NoError(t, err)
			if tt.want {
				assert.Equal(t, []string{"GET", "probe"}, events)
			} else {
				assert.Equal(t, []string{"GET", "PUT", "settle", "probe"}, events)
			}
		})
	}
	conditional := testRoleAssignment(testScope, FoundryUserRoleID)
	conditional.Properties.Condition = new("some condition")
	assert.False(t, IsRoleSufficient(conditional, testScope))
	assert.False(t, IsRoleSufficient(nil, testScope))
}

func TestAssignmentIdentityAndConflicts(t *testing.T) {
	for _, principalType := range []armauthorization.PrincipalType{
		armauthorization.PrincipalTypeUser,
		armauthorization.PrincipalTypeServicePrincipal,
	} {
		for _, conflict := range []string{"", "RoleAssignmentExists", "OtherConflict"} {
			t.Run(string(principalType)+conflict, func(t *testing.T) {
				names := []string{}
				client := newTestAccessClient(t, func(request *http.Request) (*http.Response, error) {
					if request.Method == http.MethodGet {
						return httpResponse(request, 200, map[string]any{"value": []any{}}), nil
					}
					scope, name, found := strings.Cut(
						request.URL.Path,
						"/providers/Microsoft.Authorization/roleAssignments/",
					)
					require.True(t, found)
					assert.Equal(t, testScope, scope)
					_, err := uuid.Parse(name)
					require.NoError(t, err)
					names = append(names, name)
					var body armauthorization.RoleAssignmentCreateParameters
					require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
					require.NotNil(t, body.Properties)
					assert.Equal(t, testPrincipal, *body.Properties.PrincipalID)
					assert.Equal(
						t,
						"/subscriptions/"+testSubscription+
							"/providers/Microsoft.Authorization/roleDefinitions/"+FoundryUserRoleID,
						*body.Properties.RoleDefinitionID,
					)
					assert.Equal(t, principalType, *body.Properties.PrincipalType)
					if conflict != "" {
						return httpResponse(request, 409, map[string]any{
							"error": map[string]string{"code": conflict},
						}), nil
					}
					return httpResponse(request, 201, map[string]any{}), nil
				})
				account, ok := client.Account.(*accountStub)
				require.True(t, ok)
				account.principalType = principalType
				scope, err := client.Resolve(t.Context(), testEndpoint)
				require.NoError(t, err)
				assert.Equal(t, "user-access-tenant", scope.TenantID)
				assert.Equal(t, testSubscription, account.lookupSubscription)
				for range 2 {
					confirmed, assigned, principalID, _, err := client.PrepareRole(t.Context(), scope, nil)
					assert.Equal(t, testPrincipal, principalID)
					assert.Equal(t, testSubscription, account.objectSubscription)
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

func TestAccessReadiness(t *testing.T) {
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
		{
			"new grant settles before first probe",
			false, false, false, []error{toolboxMissing}, false, 1,
			[]time.Duration{15 * time.Second},
		},
		{
			"new grant propagation",
			false, false, false, []error{denied, nil}, false, 2,
			[]time.Duration{15 * time.Second, 5 * time.Second},
		},
		{
			"new grant exhausted",
			false, false, false, []error{denied}, true, 3,
			[]time.Duration{15 * time.Second, 5 * time.Second, 5 * time.Second},
		},
		{
			"existing role propagation",
			false, true, false, []error{denied, nil}, false, 2,
			[]time.Duration{5 * time.Second},
		},
		{
			"existing role exhausted",
			false, true, false, []error{denied}, true, 3,
			[]time.Duration{5 * time.Second, 5 * time.Second},
		},
		{"ARM denied custom access succeeds", true, false, false, []error{nil}, false, 1, nil},
		{"ARM denied access fails without propagation polling", true, false, false, []error{denied}, true, 1, nil},
		{"no account denial", false, false, true, []error{denied}, true, 1, nil},
		{
			"project sync independent of ARM",
			true, false, false, []error{projectMissing, toolboxMissing}, false, 2,
			[]time.Duration{5 * time.Second},
		},
		{
			"no account project sync",
			false, false, true, []error{projectMissing, nil}, false, 2,
			[]time.Duration{5 * time.Second},
		},
		{
			"project sync exhausted",
			true, false, false, []error{projectMissing}, true, 3,
			[]time.Duration{5 * time.Second, 5 * time.Second},
		},
		{
			"project sync does not unlock auth retries",
			true, false, false, []error{projectMissing, denied}, true, 2,
			[]time.Duration{5 * time.Second},
		},
		{"policy failure authoritative", false, true, false, []error{policyDenied}, true, 1, nil},
		{"network failure authoritative", false, true, false, []error{errors.New("network failed")}, true, 1, nil},
		{
			"server failure authoritative",
			false, true, false, []error{&azcore.ResponseError{StatusCode: 500}}, true, 1, nil,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestAccessClient(t, func(request *http.Request) (*http.Response, error) {
				if tt.armDenied {
					return httpResponse(request, 403, map[string]any{
						"error": map[string]string{"code": "AuthorizationFailed"},
					}), nil
				}
				if request.Method == http.MethodGet {
					roles := []*armauthorization.RoleAssignment{}
					if tt.existing {
						roles = append(roles, testRoleAssignment(testScope, FoundryUserRoleID))
					}
					return httpResponse(request, 200, map[string]any{"value": roles}), nil
				}
				return httpResponse(request, 201, map[string]any{}), nil
			})
			if tt.noAccount {
				client.Account = nil
			}
			var waits []time.Duration
			client.Wait = func(_ context.Context, duration time.Duration) error {
				waits = append(waits, duration)
				return nil
			}
			probes := 0
			scope, err := client.Resolve(t.Context(), testEndpoint)
			require.NoError(t, err)
			messages := []string{}
			_, err = Ensure(
				t.Context(),
				client,
				scope,
				ReadRequired,
				func(message string) { messages = append(messages, message) },
				func(ctx context.Context) (*testResource, error) {
					deadline, ok := ctx.Deadline()
					assert.True(t, ok)
					assert.LessOrEqual(t, time.Until(deadline), 5*time.Minute)
					err := tt.probes[min(probes, len(tt.probes)-1)]
					probes++
					if err != nil {
						return nil, err
					}
					return &testResource{Name: "research"}, nil
				},
			)
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

func TestOptOutAndUnverifiedContext(t *testing.T) {
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
			t.Setenv(SkipRoleAssignmentsEnvKey, tt.process)
			armCalls := 0
			client := newTestAccessClient(t, func(request *http.Request) (*http.Response, error) {
				armCalls++
				return httpResponse(request, 200, map[string]any{
					"value": []*armauthorization.RoleAssignment{testRoleAssignment(testScope, FoundryUserRoleID)},
				}), nil
			})
			values := defaultEnvironmentValues()
			values[SkipRoleAssignmentsEnvKey] = tt.envValue
			if tt.unverified {
				delete(values, "AZURE_AI_PROJECT_ID")
			}
			client.Environment = environmentStub{values: values}
			scope, err := client.Resolve(t.Context(), testEndpoint)
			require.NoError(t, err)
			probes := 0
			_, err = Ensure(t.Context(), client, scope, ReadRequired, nil, func(context.Context) (*testResource, error) {
				probes++
				return nil, notFoundResponseError("toolbox not found")
			})
			require.NoError(t, err)
			assert.Equal(t, tt.wantARM, armCalls > 0)
			assert.Equal(t, 1, probes)
		})
	}
}

func TestCancellationPrecedesNotFound(t *testing.T) {
	customCanceled := errors.New("custom grpc canceled")
	client := &Client{
		IsCanceled: func(err error) bool { return errors.Is(err, customCanceled) },
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, customCanceled} {
		t.Run(cause.Error(), func(t *testing.T) {
			joined := errors.Join(notFoundResponseError("Project not found"), cause)
			_, err := Ensure(t.Context(), client, nil, ReadRequired, nil, func(context.Context) (*testResource, error) {
				return nil, joined
			})
			require.ErrorIs(t, err, cause)
			err = client.WriteError(t.Context(), joined, "get_toolbox", nil, "", "")
			require.ErrorIs(t, err, cause)
		})
	}
}

func TestReadinessBudgetRetainsServiceRemediation(t *testing.T) {
	client := newTestAccessClient(t, func(request *http.Request) (*http.Response, error) {
		return httpResponse(request, 200, map[string]any{
			"value": []*armauthorization.RoleAssignment{testRoleAssignment(testScope, FoundryUserRoleID)},
		}), nil
	})
	client.Timeout = time.Second
	client.Wait = Wait
	scope, err := client.Resolve(t.Context(), testEndpoint)
	require.NoError(t, err)
	_, err = Ensure(t.Context(), client, scope, ReadRequired, nil, func(context.Context) (*testResource, error) {
		return nil, &azcore.ResponseError{StatusCode: 403, ErrorCode: "PermissionDenied"}
	})
	sharedErr, ok := errors.AsType[*ferrors.Error](err)
	require.True(t, ok, "the internal budget should retain the Foundry failure: %v", err)
	assert.Equal(t, ferrors.CategoryService, sharedErr.Category)
	assert.Equal(t, 403, sharedErr.StatusCode)
	assert.Contains(t, sharedErr.Suggestion, "az role assignment create")

	parent, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
	defer cancel()
	_, err = Ensure(parent, client, scope, ReadRequired, nil, func(context.Context) (*testResource, error) {
		return nil, &azcore.ResponseError{StatusCode: 403, ErrorCode: "PermissionDenied"}
	})
	require.ErrorIs(t, err, context.DeadlineExceeded, "caller deadlines remain authoritative")
}

func TestStructuredRemediationDoesNotDiscloseURL(t *testing.T) {
	secretURL, err := url.Parse(
		"https://username:password@acct.services.ai.azure.com/api/projects/proj?sig=secret#fragment",
	)
	require.NoError(t, err)
	responseErr := &azcore.ResponseError{
		StatusCode: 403,
		ErrorCode:  "PermissionDenied",
		RawResponse: &http.Response{
			StatusCode: 403,
			Request:    &http.Request{URL: secretURL, Method: http.MethodGet},
			Body:       io.NopCloser(strings.NewReader("sensitive service payload")),
		},
	}
	scope := &Scope{Resource: ParseProjectResourceID(testScope)}
	var client *Client
	converted := client.WriteError(
		t.Context(),
		responseErr,
		"get_toolbox",
		scope,
		testPrincipal,
		armauthorization.PrincipalTypeUser,
	)
	sharedErr, ok := errors.AsType[*ferrors.Error](converted)
	require.True(t, ok)
	assert.Equal(t, 403, sharedErr.StatusCode)
	assert.Contains(t, sharedErr.Suggestion, "az role assignment create")
	assert.Contains(t, sharedErr.Suggestion, "--scope "+fmt.Sprintf("%q", testScope))
	assert.Contains(t, sharedErr.Suggestion, testPrincipal)
	for _, secret := range []string{"username", "password", "sig=", "secret", "fragment", "sensitive service payload"} {
		assert.NotContains(t, sharedErr.Error(), secret)
		assert.NotContains(t, sharedErr.Suggestion, secret)
	}
}

func TestPollReadinessCollectionProbe(t *testing.T) {
	denied := &azcore.ResponseError{StatusCode: 403, ErrorCode: "PermissionDenied"}
	genericNotFound := notFoundResponseError("Resource not found")

	var waits []time.Duration
	var messages []string
	probes := 0
	err := PollReadiness(t.Context(), PollOptions{
		Confirmed:                true,
		Assigned:                 true,
		Attempts:                 3,
		TreatAll404AsProjectSync: true,
		Wait: func(_ context.Context, d time.Duration) error {
			waits = append(waits, d)
			return nil
		},
		Report:       func(msg string) { messages = append(messages, msg) },
		ReadyMessage: "✓ Foundry access is ready",
	}, func(context.Context) error {
		probes++
		switch probes {
		case 1:
			return denied
		case 2:
			return genericNotFound
		default:
			return nil
		}
	})
	require.NoError(t, err)
	assert.Equal(t, 3, probes)
	assert.Equal(t, []time.Duration{15 * time.Second, 5 * time.Second, 5 * time.Second}, waits)
	assert.Equal(t, []string{
		"Waiting for Foundry access to propagate.",
		"Waiting for the Foundry project to synchronize.",
		"✓ Foundry access is ready",
	}, messages)
}

func TestHasAnyRoleAssignmentAndAssignRole(t *testing.T) {
	assert.Equal(t, "sub-123", ExtractSubscriptionID("/subscriptions/sub-123/resourceGroups/rg"))
	assert.Empty(t, ExtractSubscriptionID("/resourceGroups/rg"))
	assert.Empty(t, ExtractSubscriptionID("/subscriptions/"))

	var methods []string
	transport := transportFunc(func(request *http.Request) (*http.Response, error) {
		methods = append(methods, request.Method)
		switch request.Method {
		case http.MethodGet:
			if request.URL.Query().Get("$filter") == "atScope()" {
				return httpResponse(request, 200, map[string]any{"value": []any{}}), nil
			}
			return httpResponse(request, 200, map[string]any{
				"value": []*armauthorization.RoleAssignment{
					testRoleAssignment(testScope, FoundryUserRoleID),
				},
			}), nil
		case http.MethodPut:
			return httpResponse(request, 200, map[string]any{"properties": map[string]any{}}), nil
		default:
			t.Fatalf("unexpected method %s", request.Method)
			return nil, nil
		}
	})
	opts := &arm.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Transport: transport,
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	hasRole, err := HasAnyRoleAssignment(
		t.Context(),
		credentialStub{},
		testPrincipal,
		SufficientDataPlaneRoles,
		testScope,
		opts,
	)
	require.NoError(t, err)
	assert.True(t, hasRole)

	childOnlyOpts := &arm.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
				return httpResponse(request, 200, map[string]any{
					"value": []*armauthorization.RoleAssignment{
						testRoleAssignment(testScope+"/connections/child", FoundryUserRoleID),
					},
				}), nil
			}),
			Retry: policy.RetryOptions{MaxRetries: -1},
		},
	}
	hasChildRole, err := HasAnyRoleAssignment(
		t.Context(),
		credentialStub{},
		testPrincipal,
		SufficientDataPlaneRoles,
		testScope,
		childOnlyOpts,
	)
	require.NoError(t, err)
	assert.False(t, hasChildRole, "descendant-scope assignments must not satisfy parent scope checks")

	otherConflictOpts := &arm.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
				if request.Method == http.MethodGet {
					return httpResponse(request, 200, map[string]any{"value": []any{}}), nil
				}
				return httpResponse(request, 409, map[string]any{
					"error": map[string]string{"code": "RoleAssignmentLimitExceeded"},
				}), nil
			}),
			Retry: policy.RetryOptions{MaxRetries: -1},
		},
	}
	_, err = AssignRole(
		t.Context(),
		credentialStub{},
		testPrincipal,
		FoundryUserRoleID,
		"Foundry User",
		testScope,
		armauthorization.PrincipalTypeUser,
		otherConflictOpts,
	)
	require.Error(t, err, "non-RoleAssignmentExists 409 conflicts must not be swallowed")

	created, err := AssignRole(
		t.Context(),
		credentialStub{},
		testPrincipal,
		FoundryUserRoleID,
		"Foundry User",
		testScope,
		armauthorization.PrincipalTypeUser,
		opts,
	)
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, []string{http.MethodGet, http.MethodGet, http.MethodPut}, methods)
}

func TestWriteAuthenticationGuidance(t *testing.T) {
	var client *Client
	for _, cause := range []error{new(azidentity.AuthenticationFailedError), new(azidentity.AuthenticationRequiredError)} {
		err := client.WriteError(t.Context(), cause, "get_toolbox", nil, "", "")
		sharedErr, ok := errors.AsType[*ferrors.Error](err)
		require.True(t, ok)
		assert.Equal(t, ferrors.CategoryAuth, sharedErr.Category)
		assert.Equal(t, ferrors.CodeAuthFailed, sharedErr.Code)
		assert.Contains(t, sharedErr.Suggestion, "azd auth login")
	}
}
