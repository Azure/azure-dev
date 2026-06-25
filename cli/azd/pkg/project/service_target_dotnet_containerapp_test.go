// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
)

func TestDeploymentHost(t *testing.T) {
	tests := []struct {
		name             string
		deploymentResult *azapi.ResourceDeployment
		expectedHostType azapi.AzureResourceType
		expectedName     string
		expectError      bool
		expectedErrorMsg string
	}{
		{
			name: "ContainerApp deployment",
			deploymentResult: &azapi.ResourceDeployment{
				Resources: []*armresources.ResourceReference{
					{
						ID: new("/subscriptions/sub-id/resourceGroups/rg-name/" +
							"providers/Microsoft.App/containerApps/my-container-app"),
					},
				},
			},
			expectedHostType: azapi.AzureResourceTypeContainerApp,
			expectedName:     "my-container-app",
			expectError:      false,
		},
		{
			name: "ContainerAppJob deployment",
			deploymentResult: &azapi.ResourceDeployment{
				Resources: []*armresources.ResourceReference{
					{
						ID: new("/subscriptions/sub-id/resourceGroups/rg-name/providers/Microsoft.App/jobs/my-job"),
					},
				},
			},
			expectedHostType: azapi.AzureResourceTypeContainerAppJob,
			expectedName:     "my-job",
			expectError:      false,
		},
		{
			name: "WebSite deployment",
			deploymentResult: &azapi.ResourceDeployment{
				Resources: []*armresources.ResourceReference{
					{
						ID: new("/subscriptions/sub-id/resourceGroups/rg-name/" +
							"providers/Microsoft.Web/sites/my-web-app"),
					},
				},
			},
			expectedHostType: azapi.AzureResourceTypeWebSite,
			expectedName:     "my-web-app",
			expectError:      false,
		},
		{
			name: "Unknown resource type",
			deploymentResult: &azapi.ResourceDeployment{
				Resources: []*armresources.ResourceReference{
					{
						ID: new("/subscriptions/sub-id/resourceGroups/rg-name/" +
							"providers/Microsoft.Storage/storageAccounts/my-storage"),
					},
				},
			},
			expectError:      true,
			expectedErrorMsg: "didn't find any known application host from the deployment",
		},
		{
			name: "No resources in deployment",
			deploymentResult: &azapi.ResourceDeployment{
				Resources: []*armresources.ResourceReference{},
			},
			expectError:      true,
			expectedErrorMsg: "didn't find any known application host from the deployment",
		},
		{
			name:             "Nil deployment result",
			deploymentResult: nil,
			expectError:      true,
			expectedErrorMsg: "deployment result is empty",
		},
		{
			name: "Multiple resources with container app",
			deploymentResult: &azapi.ResourceDeployment{
				Resources: []*armresources.ResourceReference{
					{
						ID: new("/subscriptions/sub-id/resourceGroups/rg-name/" +
							"providers/Microsoft.Storage/storageAccounts/my-storage"),
					},
					{
						ID: new("/subscriptions/sub-id/resourceGroups/rg-name/" +
							"providers/Microsoft.App/containerApps/my-container-app"),
					},
				},
			},
			expectedHostType: azapi.AzureResourceTypeContainerApp,
			expectedName:     "my-container-app",
			expectError:      false,
		},
		{
			name: "Multiple resources with container app job",
			deploymentResult: &azapi.ResourceDeployment{
				Resources: []*armresources.ResourceReference{
					{
						ID: new("/subscriptions/sub-id/resourceGroups/rg-name/" +
							"providers/Microsoft.Storage/storageAccounts/my-storage"),
					},
					{
						ID: new("/subscriptions/sub-id/resourceGroups/rg-name/" +
							"providers/Microsoft.App/jobs/my-processor-job"),
					},
				},
			},
			expectedHostType: azapi.AzureResourceTypeContainerAppJob,
			expectedName:     "my-processor-job",
			expectError:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := deploymentHost(tt.deploymentResult)

			if tt.expectError {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedErrorMsg)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedHostType, result.hostType)
				require.Equal(t, tt.expectedName, result.name)
			}
		})
	}
}

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

func Test_NewDotNetContainerAppTarget(t *testing.T) {
	cli := dotnet.NewCli(exec.NewCommandRunner(nil))
	target := NewDotNetContainerAppTarget(nil, nil, nil, nil, cli, nil, nil, nil, nil, nil, nil, nil)
	require.NotNil(t, target)
}
