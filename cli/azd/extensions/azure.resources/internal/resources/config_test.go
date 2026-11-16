// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package resources

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"azure.resources/internal/resources/scaffold"
	"github.com/stretchr/testify/require"
)

func TestConfigRejectsRemovedDatabases(t *testing.T) {
	for _, resourceType := range []string{"db.postgres", "db.mysql"} {
		t.Run(resourceType, func(t *testing.T) {
			for _, mode := range []struct {
				name     string
				existing bool
			}{{name: "managed"}, {name: "existing", existing: true}} {
				t.Run(mode.name, func(t *testing.T) {
					config := Config{"database": {Type: resourceType, Existing: mode.existing}}
					require.ErrorContains(t, config.Validate(), "unsupported type")
					raw, err := config.Raw()
					require.NoError(t, err)
					_, err = Decode(raw)
					require.ErrorContains(t, err, "unsupported type")
					files, err := Generate("data", config)
					require.ErrorContains(t, err, "unsupported type")
					require.Nil(t, files)
				})
			}
		})
	}
}

func TestConfigRejectsFoundryDeploymentNameCollisions(t *testing.T) {
	for _, names := range [][]string{
		{"gpt-4o", "gpt-4o"},
		{"gpt-4o", "gpt--4o"},
		{"GPT-4O", "gpt-4o"},
	} {
		t.Run(strings.Join(names, "_"), func(t *testing.T) {
			config := Config{"foundry": {Type: AIProject, Models: []AiServicesModel{
				{Name: names[0], Version: "1", Format: "OpenAI", Sku: AiServicesModelSku{Name: "Standard"}},
				{Name: names[1], Version: "2", Format: "OpenAI", Sku: AiServicesModelSku{Name: "GlobalStandard"}},
			}}}
			raw, err := config.Raw()
			require.NoError(t, err)
			_, err = Decode(raw)
			require.ErrorContains(t, err, "same deployment name")
			_, err = Generate("data", config)
			require.ErrorContains(t, err, "same deployment name")
		})
	}
}

func TestGeneratedFoundryUserRolesIncludePrincipalIdentity(t *testing.T) {
	files, err := Generate("data", Config{"foundry": {Type: AIProject}})
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(string(files["ai-project.bicep"]),
		"guid(subscription().id, resourceGroup().id, principalId, 'localUser'"))
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name string
		raw  map[string]any
		want string
	}{
		{"empty", nil, "at least one"},
		{"unknown property", map[string]any{"db": map[string]any{"type": Mongo, "typo": true}}, "unknown field"},
		{"unsupported type", map[string]any{"db": map[string]any{"type": "db.unknown"}}, "unsupported type"},
		{"nil definition", map[string]any{"db": nil}, "unsupported type"},
		{"unsafe name", map[string]any{"../db": map[string]any{"type": Mongo}}, "resource name"},
		{"unknown Cosmos container property", map[string]any{
			"db": map[string]any{
				"type": Cosmos,
				"containers": []any{map[string]any{
					"name": "items", "partitionKeys": []string{"/id"}, "partitionKey": "/other",
				}},
			},
		}, "unknown field"},
		{"existing Container App host", map[string]any{
			"api": map[string]any{"type": ContainerApp, "existing": true},
		}, "host resource"},
		{"existing App Service host", map[string]any{
			"web": map[string]any{"type": AppService, "existing": true},
		}, "host resource"},
		{"invalid port", map[string]any{"api": map[string]any{"type": ContainerApp, "port": 70000}}, "port"},
		{"unknown link", map[string]any{
			"api": map[string]any{"type": ContainerApp, "port": 8080, "uses": []string{"elsewhere"}},
		}, "same entry"},
		{"wrong type property", map[string]any{"db": map[string]any{"type": Mongo, "port": 5432}}, "not supported"},
		{"invalid app service runtime", map[string]any{
			"web": map[string]any{
				"type": AppService, "port": 8080,
				"runtime": map[string]any{"stack": "go", "version": "1.26"},
			},
		}, "python or node"},
		{"duplicate singleton", map[string]any{
			"first": map[string]any{"type": Mongo}, "second": map[string]any{"type": Mongo},
		}, "at most one"},
		{"invalid environment name", map[string]any{
			"api": map[string]any{
				"type": ContainerApp, "port": 8080,
				"env": []any{map[string]any{"name": "BAD-KEY", "value": "x"}},
			},
		}, "environment variable name"},
		{"duplicate environment name ignoring case", map[string]any{
			"api": map[string]any{
				"type": ContainerApp, "port": 8080,
				"env": []any{
					map[string]any{"name": "TOKEN", "value": "one"},
					map[string]any{"name": "token", "value": "two"},
				},
			},
		}, "more than once"},
		{"App Service environment conflicts with generated port", map[string]any{
			"web": map[string]any{
				"type": AppService, "port": 8080,
				"runtime": map[string]any{"stack": "node", "version": "22"},
				"env":     []any{map[string]any{"name": "PORT", "value": "9000"}},
			},
		}, "conflicts with the host port"},
		{"unsafe secret literal", map[string]any{
			"api": map[string]any{
				"type": ContainerApp, "port": 8080,
				"env": []any{map[string]any{
					"name":   "VALUE",
					"secret": "not-a-substitution", // #nosec G101 -- invalid input test
				},
				},
			},
		}, "environment substitution"},
		{"Bicep symbol collision", map[string]any{
			"foo-bar":  map[string]any{"type": ContainerApp, "port": 8080},
			"foo--bar": map[string]any{"type": ContainerApp, "port": 8080},
		}, "same output name"},
		{"output collision", map[string]any{
			"secrets": map[string]any{"type": KeyVault}, "vault": map[string]any{"type": Cosmos},
		}, "same output name"},
		{"cross-type output collision", map[string]any{
			"cache": map[string]any{"type": Redis},
			"redis": map[string]any{"type": Cosmos},
		}, "same output name"},
		{"service output collision", map[string]any{
			"api-server": map[string]any{"type": ContainerApp, "port": 8080},
			"api--server": map[string]any{"type": AppService, "port": 8000,
				"runtime": map[string]any{"stack": "node", "version": "22"}},
		}, "same output name"},
		{"implicit key vault collision", map[string]any{
			"database": map[string]any{"type": Mongo},
			"vault":    map[string]any{"type": ContainerApp, "port": 8080},
		}, "deployable keyvault resource named vault"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode(tt.raw)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Decode error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestGeneratePrefixesHostEnvParameterNames(t *testing.T) {
	// LOCATION, RESOURCE_TOKEN, and API camel-case into the generated location parameter, the resourceToken
	// variable, and the api service module, so unprefixed parameters would be declared twice.
	config := Config{
		"api": {
			Type: ContainerApp, Port: 8080,
			Env: []ServiceEnvVar{
				{Name: "LOCATION", Value: "${LOCATION}"},
				{Name: "TOKEN_NAME", Value: "${RESOURCE_TOKEN}"},
				{Name: "SELF", Value: "${API}"},
			},
		},
	}
	files, err := Generate("backend", config)
	require.NoError(t, err)

	main := string(files["main.bicep"])
	resources := string(files["resources.bicep"])
	for _, name := range []string{"hostEnv_location", "hostEnv_resourceToken", "hostEnv_api"} {
		require.Contains(t, main, "param "+name+" string")
		require.Contains(t, resources, "param "+name+" string")
	}
	require.Contains(t, resources, "var resourceToken = uniqueString(")

	// The value is still the original reference so provision-time substitution reads RESOURCE_TOKEN.
	var parameters struct {
		Parameters map[string]struct {
			Value string `json:"value"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(files["main.parameters.json"], &parameters))
	require.Equal(t, "${RESOURCE_TOKEN}", parameters.Parameters["hostEnv_resourceToken"].Value)

	declaration := regexp.MustCompile(`(?m)^(?:param|var|module|resource)\s+([A-Za-z_][A-Za-z0-9_]*)\b`)
	seen := map[string]bool{}
	for _, match := range declaration.FindAllStringSubmatch(resources, -1) {
		require.False(t, seen[match[1]], "identifier %q is declared more than once in resources.bicep", match[1])
		seen[match[1]] = true
	}
}

func TestGenerateEmitsHostSecretsAsSecretReferences(t *testing.T) {
	config := Config{
		"api": {
			Type: ContainerApp, Port: 8080,
			Env: []ServiceEnvVar{{
				Name:   "TOKEN",
				Secret: "${API_TOKEN}", // #nosec G101 -- placeholder reference, not a credential
			}},
		},
	}
	files, err := Generate("backend", config)
	if err != nil {
		t.Fatal(err)
	}
	main := string(files["main.bicep"])
	resources := string(files["resources.bicep"])
	for _, expected := range []string{
		"@secure()\nparam hostEnv_apiToken string",
		"name: '" + scaffold.SecretName("TOKEN") + "'\n          value: hostEnv_apiToken",
		"name: 'TOKEN'\n            secretRef: '" + scaffold.SecretName("TOKEN") + "'",
	} {
		if !strings.Contains(main+resources, expected) {
			t.Errorf("generated secret configuration is missing %q", expected)
		}
	}
	if strings.Contains(resources, "name: 'TOKEN'\n            value: hostEnv_apiToken") {
		t.Fatal("secret host environment variables must not be emitted as readable values")
	}
}

func TestGenerateModelFreeAIProject(t *testing.T) {
	files, err := Generate("backend", Config{"foundry": {Type: AIProject}})
	if err != nil {
		t.Fatal(err)
	}
	main := string(files["main.bicep"])
	if !strings.Contains(main, "deployments: [") {
		t.Fatal("model-free AI project must receive an empty deployments array")
	}
	if strings.Contains(main, "deployments: [\n      {") {
		t.Fatal("model-free AI project must not generate a deployment")
	}
}

func TestConfigAllowsExistingAndDeployableKeyVaults(t *testing.T) {
	config := Config{
		"database":       {Type: Mongo},
		"existing-vault": {Type: KeyVault, Existing: true},
		"vault":          {Type: KeyVault},
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestServiceKeyMatchesGeneratedOutputName(t *testing.T) {
	if got, want := ServiceKey("api--server"), "API_SERVER"; got != want {
		t.Fatalf("ServiceKey() = %q, want %q", got, want)
	}
}

func TestValidateEntryNameAllowsFlatTarget(t *testing.T) {
	if err := ValidateEntryName(""); err != nil {
		t.Fatalf("ValidateEntryName(\"\") error = %v", err)
	}

	files, err := Generate("", Config{"database": {Type: Mongo}})
	if err != nil {
		t.Fatalf("Generate with an empty entry name: %v", err)
	}
	main := string(files["main.bicep"])
	if strings.Contains(main, "layerName") {
		t.Fatalf("flat-target template unexpectedly references layerName:\n%s", main)
	}
	if !strings.Contains(main, "uniqueString(subscription().id, environmentName)") {
		t.Fatal("flat-target resource group name should not include a layer identity")
	}
}

func TestDefinitionJSONProperties(t *testing.T) {
	tests := []struct {
		name string
		data string
		want any
	}{
		{"type optional, storage containers", `{"containers":["assets"]}`, []string{"assets"}},
		{"Cosmos containers", `{"type":"db.cosmos","containers":[{"name":"items","partitionKeys":["/id"]}]}`,
			[]CosmosDBContainerProps{{Name: "items", PartitionKeys: []string{"/id"}}}},
		{"App Service runtime", `{"runtime":{"stack":"python","version":"3.12"},"port":8000}`,
			AppServiceRuntime{Stack: "python", Version: "3.12"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var definition Definition
			if err := json.Unmarshal([]byte(tt.data), &definition); err != nil {
				t.Fatal(err)
			}
			switch want := tt.want.(type) {
			case []string:
				if len(definition.StorageContainers) != len(want) || definition.StorageContainers[0] != want[0] {
					t.Fatalf("storage containers = %#v, want %#v", definition.StorageContainers, want)
				}
			case []CosmosDBContainerProps:
				if len(definition.Containers) != 1 || definition.Containers[0].Name != want[0].Name ||
					strings.Join(definition.Containers[0].PartitionKeys, ",") != strings.Join(want[0].PartitionKeys, ",") {
					t.Fatalf("Cosmos containers = %#v, want %#v", definition.Containers, want)
				}
			case AppServiceRuntime:
				if definition.Runtime != want {
					t.Fatalf("runtime = %#v, want %#v", definition.Runtime, want)
				}
			}
		})
	}

	config := Config{"storage": {Type: Storage, StorageContainers: []string{"assets"}}}
	raw, err := config.Raw()
	if err != nil {
		t.Fatal(err)
	}
	if got := raw["storage"].(map[string]any)["containers"].([]any)[0]; got != "assets" {
		t.Fatalf("Raw storage containers = %v", got)
	}

	catalogRaw, err := catalogConfig().Raw()
	if err != nil {
		t.Fatal(err)
	}
	mongo := catalogRaw["mongo"].(map[string]any)
	if _, ok := mongo["runtime"]; ok {
		t.Fatal("Raw should omit the zero-valued runtime property")
	}
	if _, ok := mongo["model"]; ok {
		t.Fatal("Raw should omit the zero-valued model property")
	}
	if _, err := Decode(catalogRaw); err != nil {
		t.Fatalf("catalog Raw roundtrip failed validation: %v", err)
	}
}

func catalogConfig() Config {
	return Config{
		"cache": {
			Type: Redis,
		},
		"mongo": {
			Type: Mongo,
		},
		"document-db": {
			Type: Cosmos, Containers: []CosmosDBContainerProps{{Name: "items", PartitionKeys: []string{"/id"}}},
		},
		"api-service": {
			Type: ContainerApp, Port: 8080,
			Uses: []string{"cache", "mongo", "document-db", "chat-model", "embedding",
				"events", "bus", "storage-account", "foundry", "ai-search", "vault"},
			Env: []ServiceEnvVar{
				{Name: "WELCOME", Value: "Hello, I'm ${PUBLIC_NAME}"},
				{Name: "PASSWORD", Secret: "${DATABASE_PASSWORD}"},
			},
		},
		"web": {
			Type: AppService, Port: 8000, Runtime: AppServiceRuntime{Stack: "python", Version: "3.12"},
			StartupCommand: "python app's entry.py",
			Uses:           []string{"mongo", "storage-account"},
		},
		"chat-model": {
			Type: OpenAIModel, Model: AIModelPropsModel{Name: "gpt-4o", Version: "2024-08-06"},
		},
		"embedding": {
			Type: OpenAIModel, Model: AIModelPropsModel{Name: "text-embedding-3-small", Version: "1"},
		},
		"events": {
			Type: EventHubs, Hubs: []string{"events"},
		},
		"bus": {
			Type: ServiceBus, Queues: []string{"jobs"}, Topics: []string{"updates"},
		},
		"storage-account": {
			Type: Storage, StorageContainers: []string{"assets"},
		},
		"foundry": {
			Type: AIProject, Models: []AiServicesModel{{
				Name: "gpt-4o", Version: "2024-08-06", Format: "OpenAI",
				Sku: AiServicesModelSku{Name: "GlobalStandard", UsageName: "OpenAI.GlobalStandard", Capacity: 10},
			}},
		},
		"ai-search": {
			Type: AISearch,
		},
		"vault": {
			Type: KeyVault,
		},
	}
}

func TestGenerateFullResourceCatalog(t *testing.T) {
	config := catalogConfig()
	files, err := Generate("backend", config)
	if err != nil {
		t.Fatal(err)
	}
	main := string(files["main.bicep"])
	resources := string(files["resources.bicep"])
	parameters := string(files["main.parameters.json"])

	for _, expected := range []string{
		"br/public:avm/res/cache/redis:",
		"br/public:avm/res/document-db/database-account:",
		"br/public:avm/res/cognitive-services/account:",
		"br/public:avm/res/event-hub/namespace:",
		"br/public:avm/res/service-bus/namespace:",
		"br/public:avm/res/storage/storage-account:",
		"br/public:avm/res/search/search-service:",
		"br/public:avm/res/key-vault/vault:",
		"br/public:avm/res/app/container-app:",
		"br/public:avm/res/web/site:",
		"linuxFxVersion: 'python|3.12'",
		"appCommandLine: 'python app\\u{27}s entry.py'",
		"value: 'Hello, I\\u{27}m ${hostEnv_publicName}'",
		"PYTHON_ENABLE_GUNICORN_MULTIWORKERS",
		"name: 'items'",
		"name: 'assets'",
		"name: 'events'",
		"name: 'jobs'",
		"name: 'updates'",
		"output AZURE_RESOURCES_BACKEND__RESOURCE_MONGO_ID",
		"output AZURE_RESOURCES_BACKEND__RESOURCE_DOCUMENT_DB_ID",
		"output AZURE_RESOURCES_BACKEND__RESOURCE_CHAT_MODEL_ID",
		"output AZURE_RESOURCES_BACKEND__RESOURCE_EMBEDDING_ID",
		"output AZURE_RESOURCES_BACKEND__RESOURCE_AI_PROJECT_ID",
		"output AZURE_RESOURCES_BACKEND__RESOURCE_SEARCH_ID",
		"output AZURE_RESOURCES_BACKEND__RESOURCE_SERVICE_BUS_ID",
		"output AZURE_RESOURCES_BACKEND__RESOURCE_EVENT_HUBS_ID",
		"output AZURE_RESOURCES_BACKEND__RESOURCE_STORAGE_ID",
		"output AZURE_RESOURCES_BACKEND__RESOURCE_VAULT_ID",
		"output SERVICE_API_SERVICE_RESOURCE_GROUP",
		"output SERVICE_API_SERVICE_NAME",
		"output SERVICE_API_SERVICE_REGISTRY_ENDPOINT",
		"output SERVICE_WEB_RESOURCE_GROUP",
		"output SERVICE_WEB_NAME",
		"output SERVICE_WEB_RESOURCE_GROUP string = rg.name",
		"'azd-env-name': environmentName",
	} {
		if !strings.Contains(main+resources, expected) {
			t.Errorf("generated catalog is missing %q", expected)
		}
	}
	if strings.Contains(main+resources, "output AZURE_CONTAINER_REGISTRY_ENDPOINT") {
		t.Fatal("catalog must not emit a shared registry endpoint")
	}
	if strings.Contains(resources, "output SERVICE_WEB_REGISTRY_ENDPOINT") {
		t.Fatal("App Service must not emit a registry endpoint")
	}
	if !strings.Contains(resources, "param hostEnv_databasePassword string") ||
		!strings.Contains(resources, "@secure()\nparam hostEnv_databasePassword string") {
		t.Fatal("secret environment substitutions must use secure Bicep parameters")
	}
	if strings.Contains(parameters, "top-secret") {
		t.Fatal("generated parameter values must not contain secret material")
	}
	if strings.Contains(main+resources, "output hostEnv_databasePassword") {
		t.Fatal("secret environment parameters must not be deployment outputs")
	}

	again, err := Generate("backend", config)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(again) {
		t.Fatalf("generated file count changed: %d != %d", len(files), len(again))
	}
	for name, content := range files {
		if string(content) != string(again[name]) {
			t.Errorf("%s is not deterministic", name)
		}
	}
}

func TestGeneratedBicepFilesHaveCopyrightHeaders(t *testing.T) {
	config := catalogConfig()
	config["existing-storage"] = Definition{Type: Storage, Existing: true}
	files, err := Generate("backend", config)
	require.NoError(t, err)
	header := "// Copyright (c) Microsoft Corporation. All rights reserved.\n" +
		"// Licensed under the MIT License.\n"
	bicepFiles := 0
	for name, contents := range files {
		if strings.HasSuffix(name, ".bicep") {
			require.Truef(t, strings.HasPrefix(string(contents), header), "%s is missing its copyright header", name)
			bicepFiles++
		} else if strings.HasSuffix(name, ".json") {
			require.Truef(t, json.Valid(contents), "%s must remain valid JSON", name)
		}
	}
	require.Equal(t, 6, bicepFiles, "cover all templates and supporting Bicep modules")
}

func TestGenerateExistingResourceBindings(t *testing.T) {
	files, err := Generate("backend", Config{
		"api": {
			Type: ContainerApp, Port: 8080, Uses: []string{"existing-storage"},
		},
		"existing-storage": {
			Type: Storage, Existing: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	resources := string(files["resources.bicep"])
	parameters := string(files["main.parameters.json"])
	for _, expected := range []string{
		"resource existingExistingStorage 'Microsoft.Storage/storageAccounts@2023-05-01' existing",
		"existingExistingStorage.properties.primaryEndpoints.blob",
		"AZURE_STORAGE_EXISTING_STORAGE_ACCOUNT_NAME",
	} {
		if !strings.Contains(resources, expected) {
			t.Errorf("existing resource binding is missing %q", expected)
		}
	}
	if !strings.Contains(parameters, `"existingExistingStorageId"`) ||
		!strings.Contains(parameters, "${AZURE_RESOURCES_BACKEND__RESOURCE_EXISTING_STORAGE_ID}") {
		t.Fatal("existing resource ID must be exposed as an input parameter")
	}
	if strings.Contains(resources, "br/public:avm/res/storage/storage-account:") {
		t.Fatal("existing storage must not be redeployed")
	}
}

func TestGenerateEntryIsolation(t *testing.T) {
	config := Config{
		"database": {Type: Mongo},
		"storage":  {Type: Storage, Existing: true},
	}
	first, err := Generate("first", config)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Generate("second", config)
	if err != nil {
		t.Fatal(err)
	}
	if string(first["main.parameters.json"]) == string(second["main.parameters.json"]) {
		t.Fatal("entry identity must participate in deployment parameters")
	}
	if !strings.Contains(string(first["main.bicep"]), "uniqueString(subscription().id, environmentName, layerName)") {
		t.Fatal("entry identity must isolate the generated resource group name")
	}
	for _, entry := range []struct {
		name  string
		files map[string][]byte
	}{{"FIRST", first}, {"SECOND", second}} {
		prefix := "AZURE_RESOURCES_" + entry.name + "__"
		parameters := string(entry.files["main.parameters.json"])
		if !strings.Contains(parameters, "${"+prefix+"RESOURCE_STORAGE_ID}") {
			t.Fatal("existing-resource bindings must belong to their entry")
		}
		if !strings.Contains(string(entry.files["main.bicep"]), "output "+prefix+"KEY_VAULT_NAME string") {
			t.Fatal("vault output must belong to its entry")
		}
	}
}

func TestGenerateCatalogOutputKeysAreEntryLocal(t *testing.T) {
	seen := map[string]string{}
	for _, entry := range []string{"", "default", "data", "data-app", "data--app", "data-app-", "data-app--"} {
		files, err := Generate(entry, catalogConfig())
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for line := range strings.SplitSeq(string(files["main.bicep"]), "\n") {
			parts := strings.Fields(line)
			if len(parts) < 2 || parts[0] != "output" {
				continue
			}
			key := parts[1]
			if strings.HasPrefix(key, "SERVICE_") {
				continue
			}
			count++
			if !strings.HasPrefix(key, "AZURE_RESOURCES_") {
				t.Fatalf("unscoped provider output %s", key)
			}
			if prior, exists := seen[key]; exists {
				t.Fatalf("entries %q and %q share output %s", prior, entry, key)
			}
			seen[key] = entry
		}
		if count == 0 {
			t.Fatal("no entry-local outputs generated")
		}
	}
}

func TestGeneratedBicepCompiles(t *testing.T) {
	bicep, err := exec.LookPath("bicep")
	if err != nil {
		t.Skip("Bicep compiler not installed")
	}
	files, err := Generate("backend", catalogConfig())
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	for name, contents := range files {
		target := filepath.Join(directory, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// #nosec G204 -- The compiler comes from LookPath and arguments refer only to generated test files.
	command := exec.CommandContext(
		t.Context(), bicep, "build", filepath.Join(directory, "main.bicep"), "--stdout", "--no-restore")
	// Bicep warnings belong on stderr, not in the ARM JSON being validated.
	var diagnostics strings.Builder
	command.Stderr = &diagnostics
	output, err := command.Output()
	if err != nil {
		if bicepModulesNotCached([]byte(diagnostics.String())) {
			t.Skipf("Bicep compile skipped: AVM modules are missing from the local cache with --no-restore: %s",
				diagnostics.String())
		}
		t.Fatalf("Bicep build failed: %v\n%s", err, diagnostics.String())
	}
	var template map[string]any
	if err := json.Unmarshal(output, &template); err != nil {
		t.Fatalf("expected compiled ARM JSON: %v", err)
	}
}

func bicepModulesNotCached(output []byte) bool {
	codes := regexp.MustCompile(`Error (BCP[0-9]+):`).FindAllSubmatch(output, -1)
	if len(codes) == 0 {
		return false
	}
	hasMissingModules := false
	for _, code := range codes {
		switch string(code[1]) {
		case "BCP190":
			hasMissingModules = true
		case "BCP104", "BCP062":
			// These diagnostics cascade from modules that are absent from the local cache.
		default:
			return false
		}
	}
	return hasMissingModules
}

func TestBicepModulesNotCached(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{"missing cached module", "Error BCP190: Module has not been restored.\n", true},
		{"missing module with dependent errors",
			"Error BCP190: Module has not been restored.\n" +
				"Error BCP104: The referenced module has errors.\n" +
				"Error BCP062: The referenced declaration is not valid.\n",
			true},
		{"restore failure", "Error BCP192: Failed to restore module.\n", false},
		{"missing module and syntax error",
			"Error BCP190: Module has not been restored.\nError BCP018: Expected a colon.\n", false},
		{"successful output", "Build succeeded.\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bicepModulesNotCached([]byte(tt.output)); got != tt.want {
				t.Fatalf("bicepModulesNotCached() = %v, want %v", got, tt.want)
			}
		})
	}
}
