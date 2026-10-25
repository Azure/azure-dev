// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type removedChecksCredential struct{}

func (removedChecksCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	claims := base64.RawURLEncoding.EncodeToString([]byte(`{"oid":"operator","idtyp":"user"}`))
	return azcore.AccessToken{Token: "header." + claims + ".signature", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func TestAuthorizerOmitsRemovedChecksAndTheirExclusiveRequests(t *testing.T) {
	for _, supplied := range []bool{false, true} {
		input := contractPlanInput()
		if !supplied {
			input.StorageID, input.RegistryID = "", ""
		}
		plan, err := BuildPlan(input)
		require.NoError(t, err)
		planned := map[string]bool{}
		for _, op := range plan.Operations {
			assert.NotContains(t, []string{"P01", "P08"}, op.CheckID)
			assert.NotEqual(t, "Microsoft.Resources/subscriptions/resourceGroups/write", op.Action)
			planned[op.CheckID] = true
		}
		for _, id := range []string{"P02", "P03"} {
			assert.True(t, planned[id])
		}
		assert.False(t, planned["P09"], "only selected cross-RG deployments are bound later")
		for _, id := range []string{"P04", "P05"} {
			assert.Equal(t, !supplied, planned[id])
		}
		var scopes []string
		options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
			require.Equal(t, http.MethodGet, request.Method)
			assert.NotContains(t, request.URL.Path, "/permissions")
			require.True(t, strings.HasSuffix(request.URL.Path, "/roleAssignments"),
				"unreadable independent evidence must stop before extra requests")
			suffix := "/providers/Microsoft.Authorization/roleAssignments"
			scopes = append(scopes, strings.TrimSuffix(request.URL.Path, suffix))
			return contractARMResponse(request, http.StatusForbidden,
				`{"error":{"code":"AuthorizationFailed"}}`), nil
		})
		report, err := NewAuthorizer(removedChecksCredential{}, options).Check(t.Context(), plan,
			Facts{KubeletObjectID: "known-kubelet"})
		require.Error(t, err, "independent authorization failures still block")
		var registryGrants []Check
		var grantScopes []string
		for _, check := range report {
			assert.NotContains(t, []string{"P01", "P08"}, check.ID)
			assert.Equal(t, Unverified, check.Status)
			if check.ID == "P06" {
				grantScopes = append(grantScopes, check.Scope)
			}
			if check.ID == "P07" || check.ID == "P09" && strings.Contains(check.Expected, roleAssignmentWrite) {
				registryGrants = append(registryGrants, check)
				scope := plan.Registry.ID
				if !supplied {
					scope = plan.Registry.Parent
				}
				assert.Equal(t, scope, check.Scope)
			}
		}
		extra := 1
		if supplied {
			extra++
		}
		wantGrantScopes := []string{plan.Cluster.ID, plan.Workload.Parent}
		if supplied {
			wantGrantScopes = append(wantGrantScopes, plan.Storage.ID)
		}
		assert.ElementsMatch(t, wantGrantScopes, grantScopes)
		require.Len(t, report, len(plan.Operations)+extra+len(wantGrantScopes),
			"P06 adds the distinct G01-G03 scopes; F01 only adds the new customer-registry branch")
		require.Len(t, registryGrants, extra)
		require.Len(t, scopes, len(report))
	}
}
