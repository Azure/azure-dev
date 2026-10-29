// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

const (
	testSubscriptionID = "11111111-1111-1111-1111-111111111111"
	testTenantID       = "22222222-2222-2222-2222-222222222222"
	testPrincipalID    = "33333333-3333-3333-3333-333333333333"
	testProjectScope   = "/subscriptions/" + testSubscriptionID +
		"/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj"
	testProjectEndpoint = "https://acct.services.ai.azure.com/api/projects/proj"
)

type stubEnvReader struct {
	name   string
	values map[string]string
}

func (s *stubEnvReader) GetCurrent(
	context.Context, *azdext.EmptyRequest, ...grpc.CallOption,
) (*azdext.EnvironmentResponse, error) {
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: s.name}}, nil
}

func (s *stubEnvReader) GetValue(
	_ context.Context, req *azdext.GetEnvRequest, _ ...grpc.CallOption,
) (*azdext.KeyValueResponse, error) {
	return &azdext.KeyValueResponse{Key: req.Key, Value: s.values[req.Key]}, nil
}

type stubAccountReader struct {
	tenantID      string
	principalID   string
	principalType azdext.PrincipalType
}

func (s *stubAccountReader) LookupTenant(
	context.Context, *azdext.LookupTenantRequest, ...grpc.CallOption,
) (*azdext.LookupTenantResponse, error) {
	return &azdext.LookupTenantResponse{TenantId: s.tenantID}, nil
}

func (s *stubAccountReader) GetCurrentPrincipal(
	context.Context, *azdext.GetCurrentPrincipalRequest, ...grpc.CallOption,
) (*azdext.GetCurrentPrincipalResponse, error) {
	return &azdext.GetCurrentPrincipalResponse{
		ObjectId:      s.principalID,
		PrincipalType: s.principalType,
	}, nil
}

type staticCredential struct{}

func (staticCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

func jsonResponse(req *http.Request, statusCode int, body string) *http.Response {
	return &http.Response{
		Request:    req,
		StatusCode: statusCode,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestPrepareDeveloperAccess_AssignsAndWaitsForPropagation(t *testing.T) {
	env := &stubEnvReader{
		name: "dev",
		values: map[string]string{
			"AZURE_AI_PROJECT_ID":      testProjectScope,
			"FOUNDRY_PROJECT_ENDPOINT": testProjectEndpoint,
		},
	}
	account := &stubAccountReader{
		tenantID:      testTenantID,
		principalID:   testPrincipalID,
		principalType: azdext.PrincipalType_PRINCIPAL_TYPE_USER,
	}

	puts := 0
	probes := 0
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Host == "management.azure.com" && req.Method == http.MethodGet:
			return jsonResponse(req, http.StatusOK, `{"value":[]}`), nil
		case req.Host == "management.azure.com" && req.Method == http.MethodPut:
			puts++
			var body struct {
				Properties struct {
					PrincipalID      string `json:"principalId"`
					PrincipalType    string `json:"principalType"`
					RoleDefinitionID string `json:"roleDefinitionId"`
				} `json:"properties"`
			}
			require.NoError(t, json.NewDecoder(req.Body).Decode(&body))
			require.Equal(t, testPrincipalID, body.Properties.PrincipalID)
			require.Equal(t, "User", body.Properties.PrincipalType)
			require.True(t, strings.HasSuffix(body.Properties.RoleDefinitionID, "/"+roleAzureAIUser))
			return jsonResponse(req, http.StatusCreated, `{}`), nil
		default:
			probes++
			require.Equal(t, skillsPreviewHeaderValue, req.Header.Get("Foundry-Features"))
			if probes == 1 {
				return jsonResponse(req, http.StatusForbidden, `{"error":{"code":"UserError","message":"Identity(object id: `+
					testPrincipalID+`) does not have permissions for `+
					`Microsoft.CognitiveServices/accounts/AIServices/agents/read actions."}}`), nil
			}
			return jsonResponse(req, http.StatusOK, `{"data":[]}`), nil
		}
	})

	cred, err := prepareDeveloperAccessWithClients(
		t.Context(),
		env,
		account,
		PrepareDeveloperAccessOptions{Endpoint: testProjectEndpoint},
		developerAccessConfig{
			credentialProvider: func(tenantID string) (azcore.TokenCredential, error) {
				require.Equal(t, testTenantID, tenantID)
				return staticCredential{}, nil
			},
			clientOptions: &policy.ClientOptions{
				Transport: transport,
				Retry:     policy.RetryOptions{MaxRetries: -1},
			},
			propagationTimeout: time.Second,
			initialDelay:       time.Millisecond,
		},
	)
	require.NoError(t, err)
	require.NotNil(t, cred)
	require.Equal(t, 1, puts)
	require.Equal(t, 2, probes)
}

func TestPrepareDeveloperAccess_SkipsAssignmentWhenRoleExists(t *testing.T) {
	env := &stubEnvReader{
		name: "dev",
		values: map[string]string{
			"AZURE_AI_PROJECT_ID":       testProjectScope,
			"AZURE_AI_PROJECT_ENDPOINT": testProjectEndpoint,
		},
	}
	account := &stubAccountReader{
		tenantID:      testTenantID,
		principalID:   testPrincipalID,
		principalType: azdext.PrincipalType_PRINCIPAL_TYPE_SERVICE_PRINCIPAL,
	}

	puts := 0
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPut {
			puts++
		}
		return jsonResponse(req, http.StatusOK, `{"value":[{"properties":{"roleDefinitionId":"/subscriptions/`+
			testSubscriptionID+`/providers/Microsoft.Authorization/roleDefinitions/`+roleCognitiveServicesUser+`"}}]}`), nil
	})

	cred, err := prepareDeveloperAccessWithClients(
		t.Context(),
		env,
		account,
		PrepareDeveloperAccessOptions{Endpoint: testProjectEndpoint},
		developerAccessConfig{
			credentialProvider: func(string) (azcore.TokenCredential, error) { return staticCredential{}, nil },
			clientOptions: &policy.ClientOptions{
				Transport: transport,
				Retry:     policy.RetryOptions{MaxRetries: -1},
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, cred)
	require.Zero(t, puts)
}

func TestDeterministicRoleAssignmentName(t *testing.T) {
	name := deterministicRoleAssignmentName(testProjectScope, testPrincipalID, roleAzureAIUser)
	_, err := uuid.Parse(name)
	require.NoError(t, err)
	require.Equal(
		t,
		name,
		deterministicRoleAssignmentName(strings.ToUpper(testProjectScope), testPrincipalID, roleAzureAIUser),
	)
}
