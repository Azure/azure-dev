// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"azure.resources/internal/resources/scaffold"
)

func TestGenerateInfraSpecCatalog(t *testing.T) {
	config := catalogConfig()
	spec, err := generateInfraSpec(config)
	require.NoError(t, err)

	require.NotNil(t, spec.DbRedis)
	require.NotNil(t, spec.DbCosmosMongo)
	require.NotNil(t, spec.DbCosmos)
	assert.Equal(t, "mongo", spec.DbCosmosMongo.DatabaseName)
	require.Len(t, spec.DbCosmos.Containers, 1)
	assert.Equal(t, "items", spec.DbCosmos.Containers[0].ContainerName)
	assert.Equal(t, []string{"/id"}, spec.DbCosmos.Containers[0].PartitionKeyPaths)
	require.NotNil(t, spec.EventHubs)
	assert.Equal(t, []string{"events"}, spec.EventHubs.Hubs)
	require.NotNil(t, spec.ServiceBus)
	assert.Equal(t, []string{"jobs"}, spec.ServiceBus.Queues)
	assert.Equal(t, []string{"updates"}, spec.ServiceBus.Topics)
	require.NotNil(t, spec.StorageAccount)
	assert.Equal(t, []string{"assets"}, spec.StorageAccount.Containers)
	require.NotNil(t, spec.AiFoundryProject)
	require.Len(t, spec.AiFoundryProject.Models, 1)
	assert.Equal(t, "gpt-4o", spec.AiFoundryProject.Models[0].Name)
	require.Len(t, spec.AIModels, 2)
	require.Len(t, spec.Services, 2)

	api := spec.Services[0]
	assert.Equal(t, "api-service", api.Name)
	assert.Equal(t, 8080, api.Port)
	assert.NotNil(t, api.DbRedis)
	assert.NotNil(t, api.DbCosmosMongo)
	assert.NotNil(t, api.DbCosmos)
	assert.NotNil(t, api.EventHubs)
	assert.NotNil(t, api.ServiceBus)
	assert.NotNil(t, api.StorageAccount)
	assert.NotNil(t, api.AiFoundryProject)
	assert.NotNil(t, api.AISearch)
	assert.NotNil(t, api.KeyVault)
	assert.Len(t, api.AIModels, 2)
}

func TestGenerateAIProjectDoesNotEmitPromptMetadata(t *testing.T) {
	files, err := Generate("backend", catalogConfig())
	require.NoError(t, err)
	main := string(files["main.bicep"])
	require.Contains(t, main, "param aiDeploymentsLocation string")
	require.Contains(t, main, "location: aiDeploymentsLocation")
	require.NotContains(t, main, "@metadata")
	require.NotContains(t, main, "usageName:")
}

func TestGenerateInfraSpecAddsImplicitVaultWithoutMutatingConfig(t *testing.T) {
	for _, resourceType := range []string{Redis, Mongo} {
		t.Run(resourceType, func(t *testing.T) {
			config := Config{"database": {Type: resourceType}}
			spec, err := generateInfraSpec(config)
			require.NoError(t, err)
			require.NotNil(t, spec.KeyVault)
			require.Len(t, config, 1)
			assert.Empty(t, spec.Services)
		})
	}
}

func TestGenerateInfraSpecHostLinks(t *testing.T) {
	config := Config{
		"api": {Type: ContainerApp, Port: 8080},
		"web": {Type: ContainerApp, Port: 3000, Uses: []string{"api"}},
	}
	spec, err := generateInfraSpec(config)
	require.NoError(t, err)
	require.Len(t, spec.Services, 2)
	require.NotNil(t, spec.Services[0].Backend)
	require.Len(t, spec.Services[0].Backend.Frontends, 1)
	assert.Equal(t, "web", spec.Services[0].Backend.Frontends[0].Name)
	require.NotNil(t, spec.Services[1].Frontend)
	require.Len(t, spec.Services[1].Frontend.Backends, 1)
	assert.Equal(t, "api", spec.Services[1].Frontend.Backends[0].Name)
}

func TestGenerateAppServiceRuntimeDefaults(t *testing.T) {
	for _, stack := range []string{"python", "node", "dotnet"} {
		t.Run(stack, func(t *testing.T) {
			spec, err := generateInfraSpec(Config{
				"web": {
					Type: AppService, Port: 8000,
					Runtime:        AppServiceRuntime{Stack: stack, Version: "test-version"},
					StartupCommand: "start-app",
					Env: []ServiceEnvVar{
						{Name: "ENABLE_ORYX_BUILD", Value: "false"},
						{Name: "APP_ENV", Value: "production"},
					},
				},
			})
			require.NoError(t, err)
			require.Len(t, spec.Services, 1)
			service := spec.Services[0]
			require.NotNil(t, service.Runtime)
			assert.Equal(t, stack, service.Runtime.Type)
			assert.Equal(t, "test-version", service.Runtime.Version)
			assert.Equal(t, "start-app", service.StartupCommand)
			assert.Equal(t, 8000, service.Port)
			assert.Equal(t, "'false'", service.Env["ENABLE_ORYX_BUILD"])
			assert.Equal(t, "'true'", service.Env["SCM_DO_BUILD_DURING_DEPLOYMENT"])
			assert.Equal(t, "'production'", service.Env["APP_ENV"])
			_, pythonDefault := service.Env["PYTHON_ENABLE_GUNICORN_MULTIWORKERS"]
			assert.Equal(t, stack == "python", pythonDefault)
		})
	}
}

func TestGenerateEnvironmentSubstitutions(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		secret     bool
		want       string
		parameters []scaffold.Parameter
	}{
		{"literal", "foo", false, "'foo'", nil},
		{"multiline literal", "first\r\nsecond", false, "'first\\r\\nsecond'", nil},
		{"multiline interpolation", "first\n${WORLD}\r", false, "'first\\n${hostEnv_world}\\r'",
			[]scaffold.Parameter{{Name: "hostEnv_world", Value: "${WORLD}", Type: "string"}}},
		{"parameter", "${MY_VAR}", false, "hostEnv_myVar",
			[]scaffold.Parameter{{Name: "hostEnv_myVar", Value: "${MY_VAR}", Type: "string"}}},
		{"secure parameter", "${MY_SECRET}", true, "hostEnv_mySecret",
			[]scaffold.Parameter{{Name: "hostEnv_mySecret", Value: "${MY_SECRET}", Type: "string", Secret: true}}},
		{"embedded default", "Hello, ${WORLD:=okay}!", false, "'Hello, ${hostEnv_world}!'", []scaffold.Parameter{
			{Name: "hostEnv_world", Value: "${WORLD:=okay}", Type: "string"},
		}},
		{"multiple parameters", "${CAT} and ${DOG}", false, "'${hostEnv_cat} and ${hostEnv_dog}'",
			[]scaffold.Parameter{
				{Name: "hostEnv_cat", Value: "${CAT}", Type: "string"},
				{Name: "hostEnv_dog", Value: "${DOG}", Type: "string"},
			}},
		{"secure defaults", "${DB_HOST:='local'}:${DB_USERNAME:='okay'}", true,
			"'${hostEnv_dbHost}:${hostEnv_dbUsername}'",
			[]scaffold.Parameter{
				{Name: "hostEnv_dbHost", Value: "${DB_HOST:='local'}", Type: "string", Secret: true},
				{Name: "hostEnv_dbUsername", Value: "${DB_USERNAME:='okay'}", Type: "string", Secret: true},
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := &scaffold.InfraSpec{}
			value, err := genBicepParamsFromEnvSubst(tt.value, tt.secret, spec)
			require.NoError(t, err)
			assert.Equal(t, tt.want, value)
			assert.Equal(t, tt.parameters, spec.Parameters)
		})
	}
}

func TestGenerateReusesParametersAndPreservesSecrecy(t *testing.T) {
	spec := &scaffold.InfraSpec{}
	for _, secret := range []bool{false, true, false} {
		value, err := genBicepParamsFromEnvSubst("${TOKEN}", secret, spec)
		require.NoError(t, err)
		assert.Equal(t, "hostEnv_token", value)
	}
	require.Len(t, spec.Parameters, 1)
	assert.True(t, spec.Parameters[0].Secret)
	assert.Equal(t, "${TOKEN}", spec.Parameters[0].Value)
}

func TestGenerateValidatesRequiredModelAndRuntimeFields(t *testing.T) {
	for _, definition := range []Definition{
		{Type: OpenAIModel, Model: AIModelPropsModel{Version: "1"}},
		{Type: OpenAIModel, Model: AIModelPropsModel{Name: "gpt-4"}},
		{Type: AppService, Port: 8000, Runtime: AppServiceRuntime{Version: "3.12"}},
		{Type: AppService, Port: 8000, Runtime: AppServiceRuntime{Stack: "python"}},
	} {
		_, err := Generate("backend", Config{"resource": definition})
		require.Error(t, err)
	}
}

func TestGenerateRejectsDuplicateSingletonResources(t *testing.T) {
	for _, resourceType := range []string{
		Redis, Mongo, Cosmos, EventHubs, ServiceBus, Storage, AIProject, AISearch, KeyVault,
	} {
		t.Run(resourceType, func(t *testing.T) {
			_, err := Generate("backend", Config{
				"first": {Type: resourceType}, "second": {Type: resourceType},
			})
			require.Error(t, err)
		})
	}
}

func TestGenerateExistingHostBindings(t *testing.T) {
	tests := []struct {
		resourceType string
		azureType    string
		env          map[string]string
	}{
		{
			Cosmos, "Microsoft.DocumentDB/databaseAccounts",
			map[string]string{"AZURE_COSMOS_SHARED_ENDPOINT": "existingShared.properties.documentEndpoint"},
		},
		{
			OpenAIModel, "Microsoft.CognitiveServices/accounts",
			map[string]string{"AZURE_OPENAI_SHARED_ENDPOINT": "existingShared.properties.endpoint"},
		},
		{
			AIProject, "Microsoft.CognitiveServices/accounts/projects",
			map[string]string{
				"AZURE_AI_PROJECT_SHARED_ENDPOINT": "existingShared.properties.endpoints['AI Foundry API']",
			},
		},
		{
			AISearch, "Microsoft.Search/searchServices",
			map[string]string{"AZURE_AI_SEARCH_SHARED_ENDPOINT": "existingShared.properties.endpoint"},
		},
		{
			Storage, "Microsoft.Storage/storageAccounts",
			map[string]string{
				"AZURE_STORAGE_SHARED_ACCOUNT_NAME":  "existingShared.name",
				"AZURE_STORAGE_SHARED_BLOB_ENDPOINT": "existingShared.properties.primaryEndpoints.blob",
			},
		},
		{
			KeyVault, "Microsoft.KeyVault/vaults",
			map[string]string{
				"AZURE_KEY_VAULT_SHARED_NAME":     "existingShared.name",
				"AZURE_KEY_VAULT_SHARED_ENDPOINT": "existingShared.properties.vaultUri",
			},
		},
		{
			EventHubs, "Microsoft.EventHub/namespaces",
			map[string]string{
				"AZURE_EVENT_HUBS_SHARED_NAME": "existingShared.name",
				"AZURE_EVENT_HUBS_SHARED_HOST": "split(split(existingShared.properties.serviceBusEndpoint, '//')[1], " +
					"':')[0]",
			},
		},
		{
			ServiceBus, "Microsoft.ServiceBus/namespaces",
			map[string]string{
				"AZURE_SERVICE_BUS_SHARED_NAME": "existingShared.name",
				"AZURE_SERVICE_BUS_SHARED_HOST": "split(split(existingShared.properties.serviceBusEndpoint, '//')[1], " +
					"':')[0]",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.resourceType, func(t *testing.T) {
			config := Config{
				"api":    {Type: ContainerApp, Port: 8080, Uses: []string{"shared"}},
				"shared": {Type: tt.resourceType, Existing: true},
			}
			spec, err := generateInfraSpec(config)
			require.NoError(t, err)
			require.Len(t, spec.Services, 1)
			assert.Equal(t, tt.env, spec.Services[0].Env)
			require.Len(t, spec.Existing, 1)
			assert.Equal(t, tt.azureType, spec.Existing[0].ResourceType)

			files, err := Generate("backend", config)
			require.NoError(t, err)
			for key, value := range tt.env {
				assert.Contains(t, string(files["resources.bicep"]), key)
				assert.Contains(t, string(files["resources.bicep"]), value)
			}
		})
	}
}

func TestGenerateRejectsUnsupportedExistingDatabaseBindings(t *testing.T) {
	for _, resourceType := range []string{Redis, Mongo} {
		t.Run(resourceType, func(t *testing.T) {
			files, err := Generate("backend", Config{
				"api":    {Type: ContainerApp, Port: 8080, Uses: []string{"shared"}},
				"shared": {Type: resourceType, Existing: true},
			})
			require.ErrorContains(t, err, "generated database credentials and connection bindings are not supported")
			require.Nil(t, files)

			meta, ok := scaffold.ResourceMetaFromType(azureResourceType(resourceType))
			require.True(t, ok)
			bindings, err := meta.BicepBindings("existingShared")
			require.ErrorContains(t, err, "does not support generated host bindings")
			require.Nil(t, bindings)
		})
	}
}
