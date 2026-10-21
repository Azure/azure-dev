// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package armclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/infra"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func managementFor(t *testing.T, handler transportFunc) *Management {
	t.Helper()
	manager, err := NewManagement(credentialFunc(func(
		_ context.Context, options policy.TokenRequestOptions,
	) (azcore.AccessToken, error) {
		assert.Equal(t, []string{cloud.AzurePublic.Services[cloud.ResourceManager].Audience + "/.default"}, options.Scopes)
		return azcore.AccessToken{Token: "management-test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
	}), config.ResourceScope{SubscriptionID: "sub", ResourceGroup: "group", ResourceName: "account"},
		transportFunc(func(request *http.Request) (*http.Response, error) {
			assert.True(t, strings.HasPrefix(request.Header.Get("Authorization"), "Bearer "))
			assert.Equal(t, "management.azure.com", request.URL.Host)
			return handler(request)
		}))
	require.NoError(t, err)
	return manager
}

func selectedModel() infra.Model {
	return infra.Model{Name: "gpt-5.2", Model: "gpt-5.2", Version: "v1", Format: "OpenAI",
		SKU: "GlobalStandard", Capacity: 1}
}

func deploymentWire(state string) string {
	return `{"name":"gpt-5.2","properties":{"provisioningState":"` + state +
		`","model":{"name":"gpt-5.2","version":"v1","format":"OpenAI"}}}`
}

func TestManagementLiveCatalogPagingAndScope(t *testing.T) {
	calls := 0
	manager := managementFor(t, func(request *http.Request) (*http.Response, error) {
		calls++
		assert.Equal(t, "2025-06-01", request.URL.Query().Get("api-version"))
		assert.Equal(t, accountID+"/models", request.URL.Path)
		if calls == 1 {
			return response(request, 200, `{"value":[{"name":"gpt-5.2","version":"v1","format":"OpenAI",`+
				`"isDefaultVersion":true,"skus":[{"name":"GlobalStandard","capacity":{"default":3}}]}],`+
				`"nextLink":"https://management.azure.com`+accountID+`/models?api-version=2025-06-01&page=2"}`), nil
		}
		return response(request, 200, `{"value":[]}`), nil
	})
	rows, err := manager.Catalog(t.Context())
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.True(t, rows[0].Default)
	assert.Equal(t, int32(3), rows[0].SKUs[0].Capacity)
	assert.Equal(t, 2, calls)
	for _, next := range []string{
		"https://attacker.example" + accountID + "/models",
		"https://management.azure.com/subscriptions/other/resourceGroups/group/" +
			"providers/Microsoft.CognitiveServices/accounts/account/models",
		"https://management.azure.com" + accountID + "/listKeys",
	} {
		calls = 0
		manager := managementFor(t, func(request *http.Request) (*http.Response, error) {
			calls++
			return response(request, 200, `{"value":[],"nextLink":"`+next+`"}`), nil
		})
		_, err := manager.Catalog(t.Context())
		require.Error(t, err)
		assert.Equal(t, 1, calls, next)
		assert.NotContains(t, err.Error(), "attacker")
	}
}

func TestManagementDeploymentIdempotenceAndConflictPreflight(t *testing.T) {
	for _, state := range []string{"Succeeded", "Creating", "Failed", ""} {
		t.Run(state, func(t *testing.T) {
			calls := 0
			manager := managementFor(t, func(request *http.Request) (*http.Response, error) {
				calls++
				assert.Equal(t, http.MethodGet, request.Method)
				return response(request, 200, `{"value":[`+deploymentWire(state)+`]}`), nil
			})
			err := manager.Deploy(t.Context(), []infra.Model{selectedModel()})
			if state == "Succeeded" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			assert.Equal(t, 1, calls)
		})
	}
	calls := 0
	manager := managementFor(t, func(request *http.Request) (*http.Response, error) {
		calls++
		assert.Equal(t, http.MethodGet, request.Method)
		return response(request, 200, `{"value":[`+deploymentWire("Succeeded")+`]}`), nil
	})
	conflict := selectedModel()
	conflict.Version = "different"
	first := selectedModel()
	first.Name, first.Model = "new-model", "new-model"
	require.Error(t, manager.Deploy(t.Context(), []infra.Model{first, conflict}))
	assert.Equal(t, 1, calls)
	calls = 0
	require.NoError(t, manager.Deploy(t.Context(), nil))
	assert.Zero(t, calls)
}

func TestManagementSDKDeploymentBodyLROAndNoWriteReplay(t *testing.T) {
	calls := []string{}
	manager := managementFor(t, func(request *http.Request) (*http.Response, error) {
		calls = append(calls, request.Method+" "+request.URL.Path)
		switch {
		case request.Method == http.MethodPut:
			assert.Equal(t, "*", request.Header.Get("If-None-Match"))
			assert.Equal(t, "2025-06-01", request.URL.Query().Get("api-version"))
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			assert.JSONEq(t, `{"sku":{"name":"GlobalStandard","capacity":1},"properties":`+
				`{"model":{"name":"gpt-5.2","version":"v1","format":"OpenAI"}}}`, string(body))
			result := response(request, 201, deploymentWire("Creating"))
			result.Header.Set("Azure-AsyncOperation", "https://management.azure.com"+accountID+"/deployments/gpt-5.2/status")
			return result, nil
		case strings.HasSuffix(request.URL.Path, "/status"):
			return response(request, 200, `{"status":"Succeeded"}`), nil
		case strings.HasSuffix(request.URL.Path, "/deployments/gpt-5.2"):
			return response(request, 200, deploymentWire("Succeeded")), nil
		default:
			return response(request, 200, `{"value":[]}`), nil
		}
	})
	require.NoError(t, manager.Deploy(t.Context(), []infra.Model{selectedModel()}))
	assert.Equal(t, []string{"GET " + accountID + "/deployments", "PUT " + accountID + "/deployments/gpt-5.2",
		"GET " + accountID + "/deployments/gpt-5.2/status", "GET " + accountID + "/deployments/gpt-5.2"}, calls)
	putCalls := 0
	manager = managementFor(t, func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodPut {
			putCalls++
			return response(request, 503, `{"error":{"code":"Unavailable","message":"private-service-detail"}}`), nil
		}
		return response(request, 200, `{"value":[]}`), nil
	})
	err := manager.Deploy(t.Context(), []infra.Model{selectedModel()})
	require.Error(t, err)
	assert.Equal(t, 1, putCalls)
	assert.NotContains(t, err.Error(), "private-service-detail")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.Error(t, manager.Deploy(ctx, []infra.Model{selectedModel()}))
}

func TestManagementRejectsMalformedCatalogAndDeployment(t *testing.T) {
	for _, body := range []string{`invalid`, `{"value":[null]}`} {
		manager := managementFor(t, func(request *http.Request) (*http.Response, error) {
			return response(request, 200, body), nil
		})
		_, err := manager.Catalog(t.Context())
		require.Error(t, err)
		_, err = manager.ListDeployments(t.Context())
		require.Error(t, err)
	}
	manager := managementFor(t, func(request *http.Request) (*http.Response, error) {
		return response(request, 200, `{"value":[]}`), nil
	})
	for _, mutate := range []func(*infra.Model){
		func(m *infra.Model) { m.Name = "../bad" },
		func(m *infra.Model) { m.Version = "" },
		func(m *infra.Model) { m.Capacity = 0 },
	} {
		model := selectedModel()
		mutate(&model)
		require.Error(t, manager.Deploy(t.Context(), []infra.Model{model}))
	}
	model := selectedModel()
	require.Error(t, manager.Deploy(t.Context(), []infra.Model{model, model}))
	wire, err := json.Marshal(model)
	require.NoError(t, err)
	assert.NotContains(t, string(wire), "Kind")
}
