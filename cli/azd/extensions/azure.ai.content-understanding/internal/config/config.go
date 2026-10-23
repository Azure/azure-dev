// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"encoding/json"
	"maps"
	"os"
	"strings"
	"unicode"

	"azure.ai.content-understanding/internal/cuclient"
)

type Options struct {
	Endpoint         string
	APIVersion       string
	DefaultAnalyzer  string
	TenantID         string
	SubscriptionID   string
	ResourceGroup    string
	Resource         string
	ModelDeployments map[string]string
}

type Resolved struct {
	Environment      string
	Endpoint         string
	APIVersion       string
	DefaultAnalyzer  string
	TenantID         string
	Scope            ResourceScope
	ModelDeployments map[string]string
	Sources          map[string]string
}

// ProcessValues reads only the supported compatibility inputs, never modifies the process.
func ProcessValues() map[string]string {
	values := make(map[string]string)
	for _, key := range []string{
		"CU_ENDPOINT", "CU_API_VERSION", "CU_DEFAULT_ANALYZER", "CU_AUTH_MODE", "CU_API_KEY",
		"AZD_ENVIRONMENT", "AZURE_TENANT_ID", "AZURE_SUBSCRIPTION_ID", "AZURE_RESOURCE_GROUP",
	} {
		if value, present := os.LookupEnv(key); present {
			values[key] = value
		}
	}
	return values
}

func resolve(
	options Options, process, values map[string]string, section []byte, user map[string]json.RawMessage,
) (Resolved, error) {
	var result Resolved
	settings := make(map[string]json.RawMessage)
	if len(section) != 0 {
		if err := json.Unmarshal(section, &settings); err != nil || settings == nil {
			return result, invalid("invalid_environment_config", "azd environment config cu must be a JSON object.")
		}
	}
	for key := range settings {
		if strings.EqualFold(strings.ReplaceAll(key, "_", ""), "apiKey") {
			return result, persistedKey()
		}
	}
	if _, present := values["CU_API_KEY"]; present {
		return result, persistedKey()
	}
	if mode := values["CU_AUTH_MODE"]; mode != "" && mode != "login" {
		return result, unsupportedKey()
	}
	var err error
	result.Sources = make(map[string]string)
	setting := func(name, flag, flagName, fallback string) (string, error) {
		for _, candidate := range []struct{ value, source string }{
			{flag, "flag: --" + flagName}, {process[name], "process"}, {values[name], "azd environment"},
		} {
			if candidate.value != "" {
				if err := ValidateSetting(name, candidate.value); err != nil {
					return "", err
				}
				result.Sources[name] = candidate.source
				return candidate.value, nil
			}
		}
		value, found, err := UserSetting(user, name)
		if err != nil {
			return "", err
		}
		if found {
			result.Sources[name] = "user config"
			return value, nil
		}
		result.Sources[name] = "built-in"
		return fallback, nil
	}
	endpoint, err := setting("CU_ENDPOINT", options.Endpoint, "endpoint", "")
	if err != nil {
		return result, err
	}
	if endpoint == "" {
		return result, invalid("missing_endpoint",
			"Specify --endpoint or CU_ENDPOINT, or run "+
				"azd ai cu config set CU_ENDPOINT https://<resource>.services.ai.azure.com.")
	}
	if result.Endpoint, err = cuclient.NormalizeEndpoint(endpoint); err != nil {
		return result, err
	}
	apiVersion, err := setting("CU_API_VERSION", options.APIVersion, "api-version", cuclient.DefaultAPIVersion)
	if err != nil {
		return result, err
	}
	result.APIVersion, err = cuclient.NormalizeAPIVersion(apiVersion)
	if err != nil {
		return result, err
	}
	result.DefaultAnalyzer, err = setting("CU_DEFAULT_ANALYZER", options.DefaultAnalyzer, "analyzer", "prebuilt-layout")
	if err != nil {
		return result, err
	}
	if result.DefaultAnalyzer != "" {
		if err := cuclient.ValidateAnalyzerID(result.DefaultAnalyzer); err != nil {
			return result, err
		}
	}
	result.TenantID = first(options.TenantID, process["AZURE_TENANT_ID"], values["AZURE_TENANT_ID"])
	result.Scope, err = ResolveResourceScope(options.Resource, options.SubscriptionID, options.ResourceGroup, ResourceScope{
		SubscriptionID: first(process["AZURE_SUBSCRIPTION_ID"], values["AZURE_SUBSCRIPTION_ID"]),
		ResourceGroup:  first(process["AZURE_RESOURCE_GROUP"], values["AZURE_RESOURCE_GROUP"]),
	})
	if err != nil {
		return result, err
	}
	for _, value := range []string{result.TenantID, result.Scope.SubscriptionID, result.Scope.ResourceGroup} {
		if value != "" && (strings.TrimSpace(value) != value || strings.ContainsFunc(value, unicode.IsControl)) {
			return result, invalid("invalid_azure_scope",
				"Azure tenant and scope values must not contain surrounding space or controls.")
		}
	}
	result.ModelDeployments = make(map[string]string)
	if raw, present := settings["modelDeployments"]; present && options.ModelDeployments == nil {
		if err := json.Unmarshal(raw, &result.ModelDeployments); err != nil || result.ModelDeployments == nil {
			return result, invalid("invalid_model_deployments",
				"azd environment config cu.modelDeployments must map model names to nonempty deployment names.")
		}
	}
	if options.ModelDeployments != nil {
		result.ModelDeployments = maps.Clone(options.ModelDeployments)
	}
	if err := (cuclient.UpdateDefaultsRequest{ModelDeployments: result.ModelDeployments}).Validate(); err != nil {
		return result, err
	}
	return result, nil
}

func validateRuntime(process map[string]string) error {
	if _, present := process["CU_API_KEY"]; present {
		return unsupportedKey()
	}
	if mode := process["CU_AUTH_MODE"]; mode != "" && mode != "login" {
		return unsupportedKey()
	}
	return nil
}

func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func invalid(code, message string) error {
	return &cuclient.Error{Kind: cuclient.ErrorValidation, Code: code, Message: message}
}

func persistedKey() error {
	return invalid("persisted_api_key",
		"Remove plaintext CU_API_KEY and cu.apiKey from the selected azd environment; "+
			"only Entra authentication is supported.")
}

func unsupportedKey() error {
	return invalid("unsupported_authentication",
		"API-key authentication is not enabled. Remove CU_API_KEY and use CU_AUTH_MODE=login with azd auth login.")
}
