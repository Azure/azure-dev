// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package access

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/require"
)

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
			probe, err := CollectionReadProbe("toolboxes", "", &policy.ClientOptions{
				Retry: policy.RetryOptions{MaxRetries: -1},
				Transport: testTransport(func(request *http.Request) (*http.Response, error) {
					require.Equal(t, http.MethodGet, request.Method)
					require.True(t, strings.HasSuffix(request.URL.Path, "/toolboxes"))
					require.Equal(t, "v1", request.URL.Query().Get("api-version"))
					return testResponse(request, tt.status, string(body)), nil
				}),
			})
			require.NoError(t, err)
			state, probeErr := probe(t.Context(), Project{Target: testTarget,
				PrincipalID: testPrincipal, Credential: testCredential{}})
			if tt.want != Unknown {
				require.NoError(t, probeErr)
			}
			require.Equal(t, tt.want, state)
		})
	}
	_, err := CollectionReadProbe("../agents", "v1", nil)
	require.Error(t, err)
}
