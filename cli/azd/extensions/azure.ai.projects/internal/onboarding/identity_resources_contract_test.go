// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSuppliedIdentityReadRequiresExactMetadataAndOwningTenant(t *testing.T) {
	for _, change := range []string{"none", "missing", "forbidden", "principalId", "clientId", "tenantId", "tenant", "id"} {
		t.Run(change, func(t *testing.T) {
			f, facts := preparationContractFixture(t, false, false, false, true, true)
			id := f.plan.Hosting.ID
			body := f.resources[strings.ToLower(id)]
			if change == "missing" {
				delete(f.resources, strings.ToLower(id))
			} else if change == "id" {
				body["id"] = id + "-other"
			} else if change != "none" && change != "forbidden" {
				properties, ok := body["properties"].(map[string]any)
				require.True(t, ok)
				if change == "tenant" {
					properties["tenantId"] = "other-tenant"
				} else {
					delete(properties, change)
				}
			}
			options := f.options()
			base := options.Transport
			options.Transport = contractTransport(func(request *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, request.Method)
				assert.Equal(t, id, request.URL.Path, "use the supplied cross-subscription/RG ID, not the target RG")
				if change == "forbidden" {
					return contractARMResponse(request, 403, `{"error":{"code":"AuthorizationFailed"}}`), nil
				}
				return base.Do(request)
			})
			checker := &sdkResourceChecker{credential: contractCredential{}, options: options}
			identity, check := checker.readSuppliedIdentity(t.Context(), f.plan.Hosting, facts.TenantID)
			switch change {
			case "none":
				assert.Equal(t, Pass, check.Status)
				assert.Equal(t, facts.HostingIdentity, identity)
			case "missing", "id", "tenant":
				assert.Equal(t, Fail, check.Status)
			default:
				assert.Equal(t, Unverified, check.Status)
			}
			if change != "none" {
				assert.Empty(t, identity)
				require.Error(t, Report{check}.Err())
			}
		})
	}
}
