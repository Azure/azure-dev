// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package access

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const testTenant = "22222222-2222-2222-2222-222222222222"
const testPrincipal = "33333333-3333-3333-3333-333333333333"

type mockAccount struct{ mock.Mock }

func (a *mockAccount) ResolveTenant(ctx context.Context, subscription string) (string, error) {
	args := a.Called(ctx, subscription)
	return args.String(0), args.Error(1)
}

func (a *mockAccount) ResolvePrincipal(ctx context.Context, subscription string) (Principal, error) {
	args := a.Called(ctx, subscription)
	return args.Get(0).(Principal), args.Error(1)
}

type testCredential struct{}

func (testCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) Do(request *http.Request) (*http.Response, error) { return f(request) }

func testResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{Request: request, StatusCode: status,
		Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func newTestBackend(t *testing.T, transport testTransport) (*AzureBackend, *mockAccount) {
	t.Helper()
	account := &mockAccount{}
	account.On("ResolveTenant", mock.Anything, "11111111-1111-1111-1111-111111111111").Return(testTenant, nil)
	account.On("ResolvePrincipal", mock.Anything, "11111111-1111-1111-1111-111111111111").
		Return(Principal{ObjectID: testPrincipal, Type: "User"}, nil)
	opts := &AzureOptions{CredentialProvider: func(
		_ context.Context, subscription, tenant string,
	) (azcore.TokenCredential, error) {
		require.Equal(t, "11111111-1111-1111-1111-111111111111", subscription)
		require.Equal(t, testTenant, tenant)
		return testCredential{}, nil
	}}
	opts.ClientOptions.Transport = transport
	opts.ClientOptions.Retry.MaxRetries = -1
	backend, err := NewAzureBackend(account, opts)
	require.NoError(t, err)
	return backend, account
}

func projectResponse(t *testing.T, request *http.Request, endpoint string) *http.Response {
	t.Helper()
	require.Equal(t, "management.azure.com", request.URL.Host)
	require.Equal(t, testProjectID, request.URL.Path, "custom subdomain must not replace the ARM account name")
	body, err := json.Marshal(map[string]any{"properties": map[string]any{
		"endpoints": map[string]string{"AI Foundry API": endpoint},
	}})
	require.NoError(t, err)
	return testResponse(request, http.StatusOK, string(body))
}

func TestAzureResolveProjectIdentity(t *testing.T) {
	for _, tt := range []struct {
		name, advertised string
		wantError        bool
	}{
		{"custom subdomain", testTarget.Endpoint, false},
		{"trailing slash", testTarget.Endpoint + "/", false},
		{"same named project in different account", strings.Replace(testTarget.Endpoint, "custom-subdomain", "other", 1), true},
		{"different project", strings.Replace(testTarget.Endpoint, "/project", "/other", 1), true},
		{"credentials in advertised endpoint", testTarget.Endpoint + "?sig=secret", true},
		{"no advertised endpoint", "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			backend, account := newTestBackend(t, func(request *http.Request) (*http.Response, error) {
				return projectResponse(t, request, tt.advertised), nil
			})
			project, err := backend.Resolve(t.Context(), testTarget)
			if tt.wantError {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "secret")
				return
			}
			require.NoError(t, err)
			require.Equal(t, testTarget, project.Target)
			require.Equal(t, testPrincipal, project.PrincipalID)
			require.Equal(t, "User", project.PrincipalType)
			require.Equal(t, testTenant, project.TenantID)
			require.NotNil(t, project.Credential)
			account.AssertExpectations(t)
		})
	}
}

func TestAzureRejectsUnsafeTargetsBeforeIO(t *testing.T) {
	for _, endpoint := range []string{
		"http://acct.services.ai.azure.com/api/projects/project",
		"https://user:password@acct.services.ai.azure.com/api/projects/project",
		testTarget.Endpoint + "?sig=secret", testTarget.Endpoint + "?", testTarget.Endpoint + "#secret",
		testTarget.Endpoint + "#", testTarget.Endpoint + "/toolboxes", testTarget.Endpoint + "/../project",
		"https://acct.services.ai.azure.com:8443/api/projects/project",
		"https://acct.services.ai.azure.com/api/projects/%70roject",
	} {
		t.Run(endpoint, func(t *testing.T) {
			account := &mockAccount{}
			backend, err := NewAzureBackend(account, nil)
			require.NoError(t, err)
			_, err = backend.Resolve(t.Context(), Target{ProjectID: testProjectID, Endpoint: endpoint})
			require.Error(t, err)
			for _, secret := range []string{"password", "secret"} {
				require.NotContains(t, err.Error(), secret)
			}
			account.AssertNotCalled(t, "ResolveTenant", mock.Anything, mock.Anything)
		})
	}
}

func assignmentBody(scope, principal, condition string) map[string]any {
	return map[string]any{"properties": map[string]any{
		"scope": scope, "principalId": principal,
		"roleDefinitionId": "/subscriptions/11111111-1111-1111-1111-111111111111/" +
			"providers/Microsoft.Authorization/roleDefinitions/" + FoundryUserRoleID,
		"condition": condition,
	}}
}

func TestAzureEnsureGrant(t *testing.T) {
	for _, tt := range []struct {
		name        string
		existing    bool
		conditional bool
		mismatch    string
		conflict    bool
		reconcile   bool
		status      int
		created     bool
		wantError   bool
	}{
		{name: "create", created: true},
		{name: "existing assignment with another name", existing: true},
		{name: "compatible concurrent creation", conflict: true, reconcile: true},
		{name: "unverified conflict", conflict: true, wantError: true},
		{name: "conditional assignment is not unconditional access", existing: true, conditional: true,
			conflict: true, wantError: true},
		{name: "different principal is not reused", existing: true, mismatch: "principalId", created: true},
		{name: "different role is not reused", existing: true, mismatch: "roleDefinitionId", created: true},
		{name: "different scope is not reused", existing: true, mismatch: "scope", created: true},
		{name: "grant forbidden", status: http.StatusForbidden, wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			puts, lists := 0, 0
			backend, _ := newTestBackend(t, func(request *http.Request) (*http.Response, error) {
				if request.URL.Path == testProjectID {
					return projectResponse(t, request, testTarget.Endpoint), nil
				}
				require.True(t, strings.HasPrefix(request.URL.Path, testProjectID+"/providers/Microsoft.Authorization/roleAssignments"))
				if request.Method == http.MethodGet {
					require.Equal(t, "atScope() and principalId eq '"+testPrincipal+"'", request.URL.Query().Get("$filter"))
					lists++
					var assignments []any
					if tt.existing || (tt.reconcile && lists > 1) {
						condition := ""
						if tt.conditional {
							condition = "restricted"
						}
						assignment := assignmentBody(testProjectID, testPrincipal, condition)
						if tt.mismatch != "" {
							properties, ok := assignment["properties"].(map[string]any)
							require.True(t, ok)
							properties[tt.mismatch] = "another-value"
						}
						assignments = append(assignments, assignment)
					}
					body, err := json.Marshal(map[string]any{"value": assignments})
					require.NoError(t, err)
					return testResponse(request, http.StatusOK, string(body)), nil
				}
				require.Equal(t, http.MethodPut, request.Method)
				puts++
				var body struct {
					Properties struct {
						PrincipalID, PrincipalType, RoleDefinitionID string
					}
				}
				require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
				require.Equal(t, testPrincipal, body.Properties.PrincipalID)
				require.Equal(t, "User", body.Properties.PrincipalType)
				require.True(t, strings.HasSuffix(body.Properties.RoleDefinitionID, "/"+FoundryUserRoleID))
				if tt.conflict {
					return testResponse(request, http.StatusConflict, `{"error":{"code":"RoleAssignmentExists"}}`), nil
				}
				if tt.status != 0 {
					return testResponse(request, tt.status, `{"error":{"code":"AuthorizationFailed"}}`), nil
				}
				return testResponse(request, http.StatusCreated, `{}`), nil
			})
			project := Project{Target: testTarget, TenantID: testTenant, PrincipalID: testPrincipal, PrincipalType: "User"}
			created, err := backend.EnsureGrant(t.Context(), project)
			if tt.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.created, created)
			if tt.existing && !tt.conditional && tt.mismatch == "" {
				require.Zero(t, puts)
			} else {
				require.Equal(t, 1, puts)
			}
		})
	}
}

func TestAzureRefusesIdentityChange(t *testing.T) {
	backend, _ := newTestBackend(t, func(request *http.Request) (*http.Response, error) {
		return projectResponse(t, request, testTarget.Endpoint), nil
	})
	_, err := backend.EnsureGrant(t.Context(), Project{Target: testTarget, TenantID: testTenant,
		PrincipalID: "44444444-4444-4444-4444-444444444444", PrincipalType: "User"})
	require.ErrorContains(t, err, "identity changed")
}

func TestAzurePrincipalAndLookupFailures(t *testing.T) {
	for _, tt := range []struct {
		name          string
		principalType string
		tenantError   error
		wantType      string
	}{
		{"service principal", "ServicePrincipal", nil, "ServicePrincipal"},
		{"unsupported principal", "Unknown", nil, ""},
		{"tenant failure", "User", errors.New("tenant lookup failed"), ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			backend, account := newTestBackend(t, func(request *http.Request) (*http.Response, error) {
				return projectResponse(t, request, testTarget.Endpoint), nil
			})
			account.ExpectedCalls = nil
			account.On("ResolveTenant", mock.Anything, mock.Anything).Return(testTenant, tt.tenantError)
			account.On("ResolvePrincipal", mock.Anything, mock.Anything).
				Return(Principal{ObjectID: testPrincipal, Type: tt.principalType}, nil)
			project, err := backend.Resolve(t.Context(), testTarget)
			if tt.wantType == "" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.wantType, project.PrincipalType)
			}
		})
	}
}

func TestAzureResolutionErrors(t *testing.T) {
	for _, stage := range []string{"nil tenant", "nil principal", "principal error", "credential error",
		"nil credential", "ARM forbidden", "missing properties"} {
		t.Run(stage, func(t *testing.T) {
			backend, account := newTestBackend(t, func(request *http.Request) (*http.Response, error) {
				if stage == "ARM forbidden" {
					return testResponse(request, http.StatusForbidden, `{"error":{"code":"AuthorizationFailed"}}`), nil
				}
				return testResponse(request, http.StatusOK, `{}`), nil
			})
			failure := errors.New("test resolution error")
			switch stage {
			case "nil tenant":
				account.ExpectedCalls = nil
				account.On("ResolveTenant", mock.Anything, mock.Anything).Return("", nil)
			case "nil principal", "principal error":
				account.ExpectedCalls = nil
				account.On("ResolveTenant", mock.Anything, mock.Anything).Return(testTenant, nil)
				var principalError error
				if stage == "principal error" {
					principalError = failure
				}
				account.On("ResolvePrincipal", mock.Anything, mock.Anything).Return(Principal{}, principalError)
			case "credential error", "nil credential":
				backend.credentials = func(context.Context, string, string) (azcore.TokenCredential, error) {
					if stage == "credential error" {
						return nil, failure
					}
					return nil, nil
				}
			}
			_, err := backend.Resolve(t.Context(), testTarget)
			require.Error(t, err)
			if stage == "principal error" || stage == "credential error" {
				require.ErrorIs(t, err, failure)
			}
			if stage == "ARM forbidden" {
				azureError, ok := errors.AsType[*azcore.ResponseError](err)
				require.True(t, ok)
				require.Equal(t, http.StatusForbidden, azureError.StatusCode)
			}
		})
	}
	_, err := NewAzureBackend(nil, nil)
	require.Error(t, err)
	backend, err := NewAzureBackend(&mockAccount{}, nil)
	require.NoError(t, err)
	credential, err := backend.credentials(t.Context(), "11111111-1111-1111-1111-111111111111", testTenant)
	require.NoError(t, err, "credential construction must not request a token")
	require.NotNil(t, credential)
}

func TestAssignmentNames(t *testing.T) {
	name := assignmentName(testProjectID, testPrincipal, FoundryUserRoleID)
	_, err := uuid.Parse(name)
	require.NoError(t, err)
	require.Equal(t, name, assignmentName(strings.ToUpper(testProjectID), testPrincipal, FoundryUserRoleID))
	require.NotEqual(t, name, assignmentName(testProjectID+"other", testPrincipal, FoundryUserRoleID))
	require.NotEqual(t, name, assignmentName(testProjectID, testTenant, FoundryUserRoleID))
}

func TestAzureConcurrentGrant(t *testing.T) {
	var mu sync.Mutex
	var stored bool
	var names []string
	// Synchronize both callers after their initial GET so both attempt creation.
	initialReads := 0
	ready := make(chan struct{})
	backend, _ := newTestBackend(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == testProjectID {
			return projectResponse(t, request, testTarget.Endpoint), nil
		}
		if request.Method == http.MethodGet {
			mu.Lock()
			exists := stored
			if !exists {
				initialReads++
				if initialReads == 2 {
					close(ready)
				}
			}
			mu.Unlock()
			if !exists {
				select {
				case <-ready:
				case <-request.Context().Done():
					return nil, request.Context().Err()
				}
				return testResponse(request, http.StatusOK, `{"value":[]}`), nil
			}
			body, err := json.Marshal(map[string]any{"value": []any{assignmentBody(testProjectID, testPrincipal, "")}})
			require.NoError(t, err)
			return testResponse(request, http.StatusOK, string(body)), nil
		}
		mu.Lock()
		defer mu.Unlock()
		names = append(names, request.URL.Path)
		if stored {
			return testResponse(request, http.StatusConflict, `{"error":{"code":"RoleAssignmentExists"}}`), nil
		}
		stored = true
		return testResponse(request, http.StatusCreated, `{}`), nil
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			_, err := backend.EnsureGrant(ctx, Project{Target: testTarget, TenantID: testTenant,
				PrincipalID: testPrincipal, PrincipalType: "User"})
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	require.Len(t, names, 2)
	require.Equal(t, names[0], names[1])
}
