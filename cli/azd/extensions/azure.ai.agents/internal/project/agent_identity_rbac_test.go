// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssignRoleToIdentityRequest(t *testing.T) {
	const scope = "/subscriptions/sub-123/resourceGroups/rg/providers/" +
		"Microsoft.CognitiveServices/accounts/account/projects/project"
	const principalID = "11111111-2222-3333-4444-555555555555"
	for _, principalType := range []armauthorization.PrincipalType{
		armauthorization.PrincipalTypeUser, armauthorization.PrincipalTypeServicePrincipal,
	} {
		t.Run(string(principalType), func(t *testing.T) {
			var methods []string
			transport := storageTestTransport(func(request *http.Request) (*http.Response, error) {
				methods = append(methods, request.Method)
				body := `{"value":[]}`
				switch request.Method {
				case http.MethodGet:
					require.Equal(t, scope+"/providers/Microsoft.Authorization/roleAssignments", request.URL.Path)
					require.Equal(t, "atScope()", request.URL.Query().Get("$filter"))
				case http.MethodPut:
					require.True(t, strings.HasPrefix(request.URL.Path,
						scope+"/providers/Microsoft.Authorization/roleAssignments/"))
					var parameters armauthorization.RoleAssignmentCreateParameters
					require.NoError(t, json.NewDecoder(request.Body).Decode(&parameters))
					require.NotNil(t, parameters.Properties)
					require.Equal(t, new("/subscriptions/sub-123/providers/Microsoft.Authorization/roleDefinitions/"+
						roleAzureAIUser), parameters.Properties.RoleDefinitionID)
					require.Equal(t, new(principalID), parameters.Properties.PrincipalID)
					require.Equal(t, new(principalType), parameters.Properties.PrincipalType)
					body = `{"properties":{}}`
				default:
					t.Fatalf("unexpected request method %s", request.Method)
				}
				return &http.Response{
					StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
					Body: io.NopCloser(strings.NewReader(body)), Request: request,
				}, nil
			})
			client, err := armauthorization.NewRoleAssignmentsClient("sub-123", storageTestCredential{}, &arm.ClientOptions{
				ClientOptions: azcore.ClientOptions{Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1}},
			})
			require.NoError(t, err)

			created, err := assignRoleToIdentityWithClient(
				t.Context(), client, principalID, roleAzureAIUser, "Foundry User", scope, principalType,
			)

			require.NoError(t, err)
			require.True(t, created)
			require.Equal(t, []string{http.MethodGet, http.MethodPut}, methods)
		})
	}
}

func TestParseAgentIdentityInfo(t *testing.T) {
	tests := []struct {
		name          string
		resourceID    string
		wantAccount   string
		wantProject   string
		wantSubID     string
		wantRG        string
		wantScope     string
		wantProjScope string
		wantErr       bool
	}{
		{
			name: "valid resource ID",
			resourceID: "/subscriptions/sub-123/resourceGroups/rg-test/providers/" +
				"Microsoft.CognitiveServices/accounts/my-account/projects/my-project",
			wantAccount: "my-account",
			wantProject: "my-project",
			wantSubID:   "sub-123",
			wantRG:      "rg-test",
			wantScope: "/subscriptions/sub-123/resourceGroups/rg-test/providers/" +
				"Microsoft.CognitiveServices/accounts/my-account",
			wantProjScope: "/subscriptions/sub-123/resourceGroups/rg-test/providers/" +
				"Microsoft.CognitiveServices/accounts/my-account/projects/my-project",
			wantErr: false,
		},
		{
			name: "resource ID with extra segments",
			resourceID: "/subscriptions/aaaa-bbbb/resourceGroups/my-rg/providers/" +
				"Microsoft.CognitiveServices/accounts/acct-name/projects/proj-name/extraSegment/value",
			wantAccount: "acct-name",
			wantProject: "proj-name",
			wantSubID:   "aaaa-bbbb",
			wantRG:      "my-rg",
			wantScope: "/subscriptions/aaaa-bbbb/resourceGroups/my-rg/providers/" +
				"Microsoft.CognitiveServices/accounts/acct-name",
			wantProjScope: "/subscriptions/aaaa-bbbb/resourceGroups/my-rg/providers/" +
				"Microsoft.CognitiveServices/accounts/acct-name/projects/proj-name/extraSegment/value",
			wantErr: false,
		},
		{
			name:       "too short resource ID",
			resourceID: "/subscriptions/sub/resourceGroups/rg",
			wantErr:    true,
		},
		{
			name:       "empty string",
			resourceID: "",
			wantErr:    true,
		},
		{
			name: "missing project segment",
			resourceID: "/subscriptions/sub-123/resourceGroups/rg-test/providers/" +
				"Microsoft.CognitiveServices/accounts/my-account",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := parseAgentIdentityInfo(tt.resourceID)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantAccount, info.AccountName)
			assert.Equal(t, tt.wantProject, info.ProjectName)
			assert.Equal(t, tt.wantSubID, info.SubscriptionID)
			assert.Equal(t, tt.wantRG, info.ResourceGroup)
			assert.Equal(t, tt.wantScope, info.AccountScope)
			assert.Equal(t, tt.wantProjScope, info.ProjectScope)
		})
	}
}

func TestExtractSubscriptionID(t *testing.T) {
	tests := []struct {
		name       string
		resourceID string
		want       string
	}{
		{
			name: "valid resource ID",
			resourceID: "/subscriptions/sub-123/resourceGroups/rg-test/providers/" +
				"Microsoft.CognitiveServices/accounts/my-account",
			want: "sub-123",
		},
		{
			name:       "no subscription segment",
			resourceID: "/resourceGroups/rg-test/providers/Microsoft.CognitiveServices/accounts/my-account",
			want:       "",
		},
		{
			name:       "empty string",
			resourceID: "",
			want:       "",
		},
		{
			name:       "subscription at end with no value",
			resourceID: "/subscriptions/",
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSubscriptionID(tt.resourceID)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestConstants(t *testing.T) {
	assert.Equal(t, "53ca6127-db72-4b80-b1b0-d745d6d5456d", roleAzureAIUser)
}
