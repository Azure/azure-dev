// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_ContainerAppTemplateManifestFuncs_UrlHost(t *testing.T) {
	fns := &containerAppTemplateManifestFuncs{}

	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{"full URL", "https://myapp.azurewebsites.net/api", "myapp.azurewebsites.net"},
		{"URL with port", "https://myapp.azurewebsites.net:443/api", "myapp.azurewebsites.net"},
		{"plain hostname", "http://localhost:8080", "localhost"},
		{"hostname only", "http://myhost", "myhost"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := fns.UrlHost(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expect, result)
		})
	}
}

func Test_ContainerAppTemplateManifestFuncs_Parameter(t *testing.T) {
	t.Run("from env var", func(t *testing.T) {
		// scaffold.AzureSnakeCase("cosmosConnectionString") = "AZURE_COSMOS_CONNECTION_STRING"
		t.Setenv("AZURE_COSMOS_CONNECTION_STRING", "my-conn-string")

		fns := &containerAppTemplateManifestFuncs{
			env: environment.NewWithValues("test", nil),
		}

		result, err := fns.Parameter("cosmosConnectionString")
		require.NoError(t, err)
		assert.Equal(t, "my-conn-string", result)
	})

	t.Run("from config", func(t *testing.T) {
		// Make sure env var is cleared so we fall through to config
		// scaffold.AzureSnakeCase("someParam") = "AZURE_SOME_PARAM"
		os.Unsetenv("AZURE_SOME_PARAM")

		env := environment.NewWithValues("test", map[string]string{})
		cfg := config.NewEmptyConfig()
		cfg.Set("infra.parameters.someParam", "config-value")
		env.Config = cfg

		fns := &containerAppTemplateManifestFuncs{
			env: env,
		}

		result, err := fns.Parameter("someParam")
		require.NoError(t, err)
		assert.Equal(t, "config-value", result)
	})

	t.Run("not found", func(t *testing.T) {
		os.Unsetenv("AZURE_MISSING_PARAM")

		env := environment.NewWithValues("test", nil)
		fns := &containerAppTemplateManifestFuncs{
			env: env,
		}

		_, err := fns.Parameter("missingParam")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("non-string config value", func(t *testing.T) {
		os.Unsetenv("AZURE_NUMERIC_PARAM")

		env := environment.NewWithValues("test", nil)
		cfg := config.NewEmptyConfig()
		cfg.Set("infra.parameters.numericParam", 42)
		env.Config = cfg

		fns := &containerAppTemplateManifestFuncs{
			env: env,
		}

		_, err := fns.Parameter("numericParam")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a string")
	})
}

func Test_ContainerAppTemplateManifestFuncs_ParameterWithDefault(t *testing.T) {
	t.Run("from env dotenv", func(t *testing.T) {
		// ParameterWithDefault uses env.LookupEnv which checks dotenv first
		// scaffold.AzureSnakeCase("someParam") = "AZURE_SOME_PARAM"
		envValues := map[string]string{
			"AZURE_SOME_PARAM": "env-value",
		}
		env := environment.NewWithValues("test", envValues)

		fns := &containerAppTemplateManifestFuncs{
			env: env,
		}

		result, err := fns.ParameterWithDefault("someParam", "default-value")
		require.NoError(t, err)
		assert.Equal(t, "env-value", result)
	})

	t.Run("uses default when not in env", func(t *testing.T) {
		os.Unsetenv("AZURE_MISSING_PARAM")

		env := environment.NewWithValues("test", nil)

		fns := &containerAppTemplateManifestFuncs{
			env: env,
		}

		result, err := fns.ParameterWithDefault("missingParam", "default-value")
		require.NoError(t, err)
		assert.Equal(t, "default-value", result)
	})
}
