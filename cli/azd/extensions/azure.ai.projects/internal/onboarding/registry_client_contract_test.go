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
				assert.Equal(t, "2025-03-01-preview", request.URL.Query().Get("api-version"))
				methods = append(methods, request.Method)
				require.Equal(t, http.MethodGet, request.Method)
				body := `{"id":"` + id + `","properties":{
					"roleAssignmentMode":"` + mode + `","provisioningState":"Succeeded",
					"publicNetworkAccess":"Enabled","loginServer":"registry.azurecr.io"}}`
				return contractARMResponse(request, http.StatusOK, body), nil
			})
			client, err := armcontainerregistry.NewRegistriesClient(
				"sub", contractCredential{}, onboardingARMOptions(options))
			require.NoError(t, err)
			resource, err := client.Get(t.Context(), "rg", "registry", nil)
			require.NoError(t, err)
			require.NotNil(t, resource.Properties.RoleAssignmentMode)
			assert.Equal(t, mode, string(*resource.Properties.RoleAssignmentMode))
			abac, check := registryResourceCheck(resource.Registry)
			assert.Equal(t, Pass, check.Status)
			assert.Equal(t, mode == "AbacRepositoryPermissions", abac)
			assert.Equal(t, []string{http.MethodGet}, methods)
		})
	}
}

func TestRegistryARMDoesNotInferMissingAuthorizationMode(t *testing.T) {
	var resource armcontainerregistry.Registry
	require.NoError(t, json.Unmarshal([]byte(`{"properties":{
		"provisioningState":"Succeeded","publicNetworkAccess":"Enabled","loginServer":"registry.azurecr.io"}}`), &resource))
	assert.Nil(t, resource.Properties.RoleAssignmentMode)
	_, check := registryResourceCheck(resource)
	assert.Equal(t, Unverified, check.Status)
}
