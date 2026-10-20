// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistryARMDocumentedAuthorizationModeContract(t *testing.T) {
	const id = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ContainerRegistry/registries/registry"
	for _, mode := range []string{"LegacyRegistryPermissions", "AbacRepositoryPermissions"} {
		t.Run(mode, func(t *testing.T) {
			var methods []string
			options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
				assert.Equal(t, id, request.URL.Path)
				assert.Equal(t, "2025-11-01", request.URL.Query().Get("api-version"))
				methods = append(methods, request.Method)
				if request.Method == http.MethodPut {
					var body struct {
						Properties registryCreateProperties `json:"properties"`
					}
					require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
					assert.Equal(t, mode, body.Properties.RoleAssignmentMode)
					assert.False(t, body.Properties.AdminUserEnabled)
					assert.False(t, body.Properties.AnonymousPullEnabled)
					assert.Equal(t, "Enabled", body.Properties.PublicNetworkAccess)
				} else {
					require.Equal(t, http.MethodGet, request.Method)
				}
				body := `{"id":"` + id + `","properties":{
					"roleAssignmentMode":"` + mode + `","provisioningState":"Succeeded",
					"publicNetworkAccess":"Enabled","loginServer":"registry.azurecr.io"}}`
				return contractARMResponse(request, http.StatusOK, body), nil
			})
			client, err := newRegistryARMClient(contractCredential{}, options)
			require.NoError(t, err)
			require.NoError(t, client.create(t.Context(), id, registryCreateRequest{
				Location: "eastus", SKU: &armcontainerregistry.SKU{Name: new(armcontainerregistry.SKUNamePremium)},
				Properties: &registryCreateProperties{RoleAssignmentMode: mode, PublicNetworkAccess: "Enabled"},
			}))
			resource, err := client.get(t.Context(), id)
			require.NoError(t, err)
			require.NotNil(t, resource.AuthorizationMode)
			assert.Equal(t, mode, *resource.AuthorizationMode)
			abac, check := registryResourceCheck(resource)
			assert.Equal(t, Pass, check.Status)
			assert.Equal(t, mode == "AbacRepositoryPermissions", abac)
			assert.Equal(t, []string{http.MethodPut, http.MethodGet}, methods)
		})
	}
}

func TestRegistryARMDoesNotInferMissingAuthorizationMode(t *testing.T) {
	var resource registryResource
	require.NoError(t, json.Unmarshal([]byte(`{"properties":{
		"provisioningState":"Succeeded","publicNetworkAccess":"Enabled","loginServer":"registry.azurecr.io"}}`), &resource))
	assert.Nil(t, resource.AuthorizationMode)
	_, check := registryResourceCheck(resource)
	assert.Equal(t, Unverified, check.Status)
}
