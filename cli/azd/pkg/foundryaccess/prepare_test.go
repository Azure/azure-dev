// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundryaccess

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type prepareEnvironment struct {
	values map[string]string
	names  []string
	fail   string
}

func (e *prepareEnvironment) GetCurrent(
	context.Context, *azdext.EmptyRequest, ...grpc.CallOption,
) (*azdext.EnvironmentResponse, error) {
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: "default"}}, nil
}

func (e *prepareEnvironment) GetValue(
	_ context.Context, request *azdext.GetEnvRequest, _ ...grpc.CallOption,
) (*azdext.KeyValueResponse, error) {
	e.names = append(e.names, request.EnvName)
	if request.Key == e.fail {
		return nil, errors.New("environment read failed")
	}
	return &azdext.KeyValueResponse{Value: e.values[request.Key]}, nil
}

type mockEnsurer struct{ mock.Mock }

func (m *mockEnsurer) Ensure(ctx context.Context, request Request) (Result, error) {
	args := m.Called(ctx, request)
	return args.Get(0).(Result), args.Error(1)
}

func TestPrepareEnvironment(t *testing.T) {
	for _, tt := range []struct {
		name, environment, endpoint, skip, fail string
		autoGrant, wantCall, wantWarning        bool
	}{
		{name: "read only", wantCall: true},
		{name: "mutation", autoGrant: true, wantCall: true},
		{name: "selected environment", environment: "staging", wantCall: true},
		{name: "skip setting", skip: "true"},
		{name: "different explicit endpoint", endpoint: "https://other.services.ai.azure.com/api/projects/project"},
		{name: "unknown environment value", fail: "AZURE_AI_PROJECT_ID", wantWarning: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			environment := &prepareEnvironment{values: map[string]string{
				"AZURE_AI_PROJECT_ID": testProjectID, "FOUNDRY_PROJECT_ENDPOINT": testTarget.Endpoint,
				"AZD_AGENT_SKIP_ROLE_ASSIGNMENTS": tt.skip,
			}, fail: tt.fail}
			ensurer := &mockEnsurer{}
			endpoint := tt.endpoint
			if endpoint == "" {
				endpoint = testTarget.Endpoint
			}
			if tt.wantCall {
				ensurer.On("Ensure", mock.Anything, mock.MatchedBy(func(request Request) bool {
					wantPolicy := CheckOnly
					if tt.autoGrant {
						wantPolicy = GrantFoundryUser
					}
					return request.Target == testTarget && request.Operation == "toolboxes.read" &&
						request.Policy == wantPolicy && request.Probe != nil
				})).Return(Result{State: Allowed, Project: Project{Credential: testCredential{}}}, nil).Once()
			}
			calls, warnings := 0, 0
			credential, err := prepare(t.Context(), environment, PrepareOptions{
				Endpoint: endpoint, EnvironmentName: tt.environment, Collection: "toolboxes", AutoGrant: tt.autoGrant,
				Warn: func(string) { warnings++ },
			}, func() (accessEnsurer, error) { calls++; return ensurer, nil })
			require.NoError(t, err)
			if tt.wantCall {
				require.Equal(t, 1, calls)
				require.NotNil(t, credential)
			} else {
				require.Zero(t, calls)
				require.Nil(t, credential)
			}
			require.Equal(t, tt.wantWarning, warnings > 0)
			wantEnv := tt.environment
			if wantEnv == "" {
				wantEnv = "default"
			}
			for _, name := range environment.names {
				require.Equal(t, wantEnv, name)
			}
			ensurer.AssertExpectations(t)
		})
	}
}

func TestPrepareGrantFailureIsWarningOnly(t *testing.T) {
	environment := &prepareEnvironment{values: map[string]string{
		"AZURE_AI_PROJECT_ID": testProjectID, "AZURE_AI_PROJECT_ENDPOINT": testTarget.Endpoint,
	}}
	ensurer := &mockEnsurer{}
	ensurer.On("Ensure", mock.Anything, mock.Anything).
		Return(Result{State: Denied}, errors.New("credential-bearing URL ?sig=secret")).Once()
	var warnings []string
	credential, err := prepare(t.Context(), environment, PrepareOptions{
		Endpoint: testTarget.Endpoint, Collection: "skills", AutoGrant: true,
		Warn: func(value string) { warnings = append(warnings, value) },
	}, func() (accessEnsurer, error) { return ensurer, nil })
	require.NoError(t, err)
	require.Nil(t, credential)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "Foundry User")
	require.NotContains(t, warnings[0], "secret")
	ensurer.AssertExpectations(t)
}

func TestPrepareCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := prepare(ctx, &prepareEnvironment{}, PrepareOptions{}, func() (accessEnsurer, error) {
		t.Fatal("canceled access check must not construct clients")
		return nil, nil
	})
	require.ErrorIs(t, err, context.Canceled)
}

func TestCollectionReadProbe(t *testing.T) {
	for _, tt := range []struct {
		name, principal, action, code string
		status                        int
		want                          State
	}{
		{"allowed", "", "", "", http.StatusOK, Allowed},
		{"confirmed denial", testPrincipal, "Microsoft.CognitiveServices/accounts/AIServices/agents/read",
			"UserError", http.StatusForbidden, Denied},
		{"another principal", testTenant, "Microsoft.CognitiveServices/accounts/AIServices/agents/read",
			"UserError", http.StatusForbidden, Unknown},
		{"network forbidden", testPrincipal, "PublicNetworkAccessDisabled", "Forbidden", http.StatusForbidden, Unknown},
		{"write denial is not a read probe", testPrincipal, "Microsoft.CognitiveServices/accounts/AIServices/agents/write",
			"UserError", http.StatusForbidden, Unknown},
		{"not found is not proof of access", "", "", "", http.StatusNotFound, Unknown},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"error": map[string]any{
				"code": tt.code, "innerError": map[string]any{"code": "ForbiddenError"},
				"message": "Identity(object id: " + tt.principal + ") does not have permissions for " + tt.action + " actions.",
			}})
			require.NoError(t, err)
			probe := collectionReadProbeWithOptions("toolboxes", "", &policy.ClientOptions{
				Retry: policy.RetryOptions{MaxRetries: -1},
				Transport: testTransport(func(request *http.Request) (*http.Response, error) {
					require.Equal(t, http.MethodGet, request.Method)
					require.True(t, strings.HasSuffix(request.URL.Path, "/toolboxes"))
					return testResponse(request, tt.status, string(body)), nil
				}),
			})
			state, probeErr := probe(t.Context(), Project{Target: testTarget,
				PrincipalID: testPrincipal, Credential: testCredential{}})
			if tt.want != Unknown {
				require.NoError(t, probeErr)
			}
			require.Equal(t, tt.want, state)
		})
	}
}
