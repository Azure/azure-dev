// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"encoding/json"
	"errors"
	"testing"

	"azure.ai.content-understanding/internal/cuclient"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolvePrecedence(t *testing.T) {
	for _, tt := range []struct {
		name    string
		options Options
		process map[string]string
		values  map[string]string
		config  string
		want    map[string]string
	}{
		{"explicit", Options{
			Endpoint: "https://FLAG.example/", APIVersion: cuclient.PreviewAPIVersion, DefaultAnalyzer: "flag-analyzer",
			TenantID: "flag-tenant", SubscriptionID: "flag-sub", ResourceGroup: "flag-rg",
		}, map[string]string{
			"CU_ENDPOINT": "https://process.example", "CU_API_VERSION": cuclient.DefaultAPIVersion,
			"CU_DEFAULT_ANALYZER": "process-analyzer", "AZURE_TENANT_ID": "process-tenant",
			"AZURE_SUBSCRIPTION_ID": "process-sub", "AZURE_RESOURCE_GROUP": "process-rg",
		}, nil, `{"apiVersion":false,"defaultAnalyzer":false}`, map[string]string{
			"endpoint": "https://flag.example/contentunderstanding", "api": cuclient.PreviewAPIVersion,
			"analyzer": "flag-analyzer", "tenant": "flag-tenant", "sub": "flag-sub", "rg": "flag-rg",
		}},
		{"process", Options{}, map[string]string{
			"CU_ENDPOINT": "https://process.example", "CU_API_VERSION": cuclient.PreviewAPIVersion,
			"CU_DEFAULT_ANALYZER": "process-analyzer", "AZURE_TENANT_ID": "process-tenant",
			"AZURE_SUBSCRIPTION_ID": "process-sub", "AZURE_RESOURCE_GROUP": "process-rg",
		}, map[string]string{"CU_ENDPOINT": "https://env.example"},
			`{"apiVersion":false,"defaultAnalyzer":false}`, map[string]string{
				"endpoint": "https://process.example/contentunderstanding", "api": cuclient.PreviewAPIVersion,
				"analyzer": "process-analyzer", "tenant": "process-tenant", "sub": "process-sub", "rg": "process-rg",
			}},
		{"environment config", Options{}, nil, map[string]string{
			"CU_ENDPOINT": "https://env.example/contentunderstanding/", "FOUNDRY_ENDPOINT": "https://foundry.example",
			"CU_API_VERSION": cuclient.DefaultAPIVersion, "CU_DEFAULT_ANALYZER": "env-analyzer",
			"AZURE_TENANT_ID": "env-tenant", "AZURE_SUBSCRIPTION_ID": "env-sub", "AZURE_RESOURCE_GROUP": "env-rg",
		}, `{"apiVersion":"2026-06-01-preview","defaultAnalyzer":"config-analyzer"}`, map[string]string{
			"endpoint": "https://env.example/contentunderstanding", "api": cuclient.PreviewAPIVersion,
			"analyzer": "config-analyzer", "tenant": "env-tenant", "sub": "env-sub", "rg": "env-rg",
		}},
		{"environment values", Options{}, nil, map[string]string{
			"FOUNDRY_ENDPOINT": "https://foundry.example", "CU_API_VERSION": cuclient.PreviewAPIVersion,
			"CU_DEFAULT_ANALYZER": "env-analyzer",
		}, "", map[string]string{
			"endpoint": "https://foundry.example/contentunderstanding", "api": cuclient.PreviewAPIVersion,
			"analyzer": "env-analyzer", "tenant": "", "sub": "", "rg": "",
		}},
		{"defaults", Options{Endpoint: "https://explicit.example"}, nil, nil, "", map[string]string{
			"endpoint": "https://explicit.example/contentunderstanding", "api": cuclient.DefaultAPIVersion,
			"analyzer": "", "tenant": "", "sub": "", "rg": "",
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result, err := resolve(tt.options, tt.process, tt.values, []byte(tt.config))
			require.NoError(t, err)
			assert.Equal(t, tt.want, map[string]string{
				"endpoint": result.Endpoint, "api": result.APIVersion, "analyzer": result.DefaultAnalyzer,
				"tenant": result.TenantID, "sub": result.Scope.SubscriptionID, "rg": result.Scope.ResourceGroup,
			})
			assert.NotNil(t, result.ModelDeployments)
		})
	}
}

func TestResolveInvalidConfiguration(t *testing.T) {
	for _, tt := range []struct {
		name    string
		options Options
		process map[string]string
		values  map[string]string
		config  string
		code    string
	}{
		{"missing endpoint", Options{}, nil, nil, "", "missing_endpoint"},
		{"bad explicit endpoint", Options{Endpoint: "http://private-value.example"}, nil, nil, "", "invalid_endpoint"},
		{"bad process endpoint", Options{}, map[string]string{"CU_ENDPOINT": " "},
			map[string]string{"CU_ENDPOINT": "https://env.example"}, "", "invalid_endpoint"},
		{"bad explicit version", Options{APIVersion: "private-value"}, nil, nil, "", "unsupported_api_version"},
		{"bad process version", Options{}, map[string]string{"CU_API_VERSION": "private-value"}, nil,
			`{"apiVersion":"2025-11-01"}`, "unsupported_api_version"},
		{"bad environment version", Options{}, nil, map[string]string{"CU_API_VERSION": "private-value"},
			"", "unsupported_api_version"},
		{"invalid JSON", Options{}, nil, nil, `{"apiVersion":private-value}`, "invalid_environment_config"},
		{"array section", Options{}, nil, nil, `[]`, "invalid_environment_config"},
		{"null section", Options{}, nil, nil, `null`, "invalid_environment_config"},
		{"null version", Options{}, nil, nil, `{"apiVersion":null}`, "invalid_environment_config"},
		{"numeric version", Options{}, nil, nil, `{"apiVersion":15}`, "invalid_environment_config"},
		{"blank version", Options{}, nil, nil, `{"apiVersion":" "}`, "invalid_environment_config"},
		{"invalid explicit analyzer", Options{DefaultAnalyzer: "private-value/secret"}, nil, nil,
			"", "invalid_analyzer_id"},
		{"invalid process analyzer", Options{}, map[string]string{"CU_DEFAULT_ANALYZER": " "}, nil,
			`{"defaultAnalyzer":"valid"}`, "invalid_analyzer_id"},
		{"invalid config analyzer", Options{}, nil, nil,
			`{"defaultAnalyzer":"private-value/secret"}`, "invalid_analyzer_id"},
		{"numeric analyzer", Options{}, nil, nil, `{"defaultAnalyzer":5}`, "invalid_environment_config"},
		{"null analyzer", Options{}, nil, nil, `{"defaultAnalyzer":null}`, "invalid_environment_config"},
		{"invalid tenant", Options{TenantID: " private-value"}, nil, nil, "", "invalid_azure_scope"},
		{"invalid scope", Options{SubscriptionID: "private-value\n"}, nil, nil, "", "invalid_azure_scope"},
		{"invalid resource group", Options{ResourceGroup: " "}, nil, nil, "", "invalid_azure_scope"},
		{"persisted dotenv key", Options{}, nil, map[string]string{"CU_API_KEY": "private-value"},
			"", "persisted_api_key"},
		{"empty persisted key", Options{}, nil, map[string]string{"CU_API_KEY": ""}, "", "persisted_api_key"},
		{"persisted config key", Options{}, nil, nil, `{"apiKey":"private-value"}`, "persisted_api_key"},
		{"legacy config key", Options{}, nil, nil, `{"api_key":"private-value"}`, "persisted_api_key"},
		{"persisted auth mode", Options{}, nil, map[string]string{"CU_AUTH_MODE": "key"},
			"", "unsupported_authentication"},
		{"model array", Options{}, nil, nil, `{"modelDeployments":[]}`, "invalid_model_deployments"},
		{"model null", Options{}, nil, nil, `{"modelDeployments":null}`, "invalid_model_deployments"},
		{"numeric deployment", Options{}, nil, nil, `{"modelDeployments":{"gpt":5}}`, "invalid_model_deployments"},
		{"blank model", Options{}, nil, nil, `{"modelDeployments":{" ":"private-value"}}`, "invalid_model_deployment"},
		{"blank deployment", Options{}, nil, nil, `{"modelDeployments":{"gpt":" "}}`, "invalid_model_deployment"},
		{"scope conflict", Options{
			Resource:       "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/account",
			SubscriptionID: "other",
		}, nil, nil, "", "conflicting_subscription"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name != "missing endpoint" && tt.options.Endpoint == "" && tt.process["CU_ENDPOINT"] == "" {
				tt.options.Endpoint = "https://explicit.example"
			}
			_, err := resolve(tt.options, tt.process, tt.values, []byte(tt.config))
			domain, ok := errors.AsType[*cuclient.Error](err)
			require.True(t, ok, "expected validation error: %v", err)
			assert.Equal(t, tt.code, domain.Code)
			assert.Equal(t, cuclient.ErrorValidation, domain.Kind)
			assert.NotContains(t, err.Error(), "private-value")
		})
	}
}

func TestResolveModelDeployments(t *testing.T) {
	config := []byte(`{"modelDeployments":{"gpt-4.1":"env-gpt"},"futureSetting":"not-returned"}`)
	result, err := resolve(Options{Endpoint: "https://explicit.example"}, nil, nil, config)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"gpt-4.1": "env-gpt"}, result.ModelDeployments)
	explicit := map[string]string{"gpt-4.1-mini": "flag-mini"}
	result, err = resolve(Options{Endpoint: "https://explicit.example", ModelDeployments: explicit},
		nil, nil, []byte(`{"modelDeployments":false}`))
	require.NoError(t, err)
	explicit["gpt-4.1-mini"] = "changed"
	assert.Equal(t, map[string]string{"gpt-4.1-mini": "flag-mini"}, result.ModelDeployments)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "futureSetting")
}

func TestRuntimeAuthentication(t *testing.T) {
	for _, process := range []map[string]string{
		{"CU_API_KEY": "private-value"}, {"CU_API_KEY": ""},
		{"CU_AUTH_MODE": "key"}, {"CU_AUTH_MODE": "private-value"}, {"CU_AUTH_MODE": "LOGIN"},
		{"CU_AUTH_MODE": "login", "CU_API_KEY": "private-value"},
	} {
		err := validateRuntime(process)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "API-key authentication is not enabled")
		assert.NotContains(t, err.Error(), "private-value")
	}
	assert.NoError(t, validateRuntime(nil))
	assert.NoError(t, validateRuntime(map[string]string{"CU_AUTH_MODE": "login"}))
}

func TestProcessValues(t *testing.T) {
	t.Setenv("CU_ENDPOINT", "https://process.example")
	t.Setenv("AZURE_TENANT_ID", "process-tenant")
	t.Setenv("AZD_ENVIRONMENT", "dev")
	t.Setenv("UNRELATED_PRIVATE_KEY", "private-value")
	result := ProcessValues()
	assert.Equal(t, "https://process.example", result["CU_ENDPOINT"])
	assert.Equal(t, "process-tenant", result["AZURE_TENANT_ID"])
	assert.Equal(t, "dev", result["AZD_ENVIRONMENT"])
	assert.NotContains(t, result, "UNRELATED_PRIVATE_KEY")
}
