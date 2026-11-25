// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package infra

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"azure.ai.content-understanding/internal/testutil"

	"github.com/drone/envsubst"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func choices() Choices {
	return Choices{Environment: "dev", Location: "eastus2", APIVersion: "2025-11-01",
		ModelSelection: "none", SubscriptionID: "sub", TenantID: "tenant"}
}

func TestCanonicalInfrastructureAssets(t *testing.T) {
	// Source asset hashes, with native hook and deployment-output adaptations.
	golden := map[string]string{
		"azure.yaml":                  "1d88ddc6e2fecf9e4ac03e277c401a7db6417c43dee3b4fc2d7b3a4a719f73d8",
		"hooks/postprovision.ps1":     "c52122fa5d528fc481c8d28a41ce652abad9c0562bfa707d4a8c83718cffead6",
		"hooks/postprovision.sh":      "8929a55610183fd0400af38463d7812a17c979a207de8d346068f721e07f59f8",
		"infra/main.bicep":            "b1174d9bf97276dc25e2aba92c184b01e34dd278c6ea3f830cf8ed86a4f554ae",
		"infra/models.json":           "37517e5f3dc66819f61f5a7bb8ace1921282415f10551d2defa5c3eb0985b570",
		"infra/modules/foundry.bicep": "18fd2b5d1ca12e75946ae317a08857f61fc5cdb05b329337036345521630c212",
	}
	directory := filepath.Join(testutil.TempDir(t), "project with spaces")
	result, err := Materialize(t.Context(), directory, choices(), false)
	require.NoError(t, err)
	assert.Len(t, result.Files, 11)
	assert.False(t, result.ReusedExistingTemplate)
	assert.Equal(t, []string{"cd '" + directory + "'", "azd auth login", "azd up"}, result.NextSteps)
	for relative, expected := range golden {
		body, err := os.ReadFile(filepath.Join(directory, relative)) // #nosec G304 -- Private test project.
		require.NoError(t, err)
		hash := sha256.Sum256(body)
		assert.Equal(t, expected, hex.EncodeToString(hash[:]), relative)
	}
	body, err := os.ReadFile(filepath.Join(directory, "README.md")) // #nosec G304 -- Private test project.
	require.NoError(t, err)
	assert.Contains(t, string(body), "`azd ai cu` extension")
	assert.Contains(t, string(body), "Provisioning and postprovision setup use azd / Entra ID authentication.")
	assert.Contains(t, string(body), "`CU_API_KEY` applies only to separate CU data-plane commands")
	assert.Contains(t, string(body), "azd env config set cu.modelSelection <selection>")
	assert.NotContains(t, string(body), "The native extension is Entra-only")
	assert.NotContains(t, string(body), "Decline every model confirmation")
	assert.NotContains(t, string(body), "Runtime key injection")
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(directory, "hooks", "postprovision.sh"))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o111), info.Mode().Perm()&0o111)
	}
}

func TestProvisionedOutputContract(t *testing.T) {
	body, err := assets.ReadFile("assets/infra/main.bicep")
	require.NoError(t, err)
	outputs := map[string]string{}
	for line := range strings.SplitSeq(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == "output" {
			require.GreaterOrEqual(t, len(fields), 3)
			outputs[fields[1]] = fields[2]
		}
	}
	assert.Equal(t, map[string]string{
		"_CU_PROVISIONED_LOCATION": "string", "_CU_PROVISIONED_RESOURCE_GROUP": "string",
		"_CU_PROVISIONED_TENANT_ID": "string", "_CU_PROVISIONED_SUBSCRIPTION_ID": "string",
		"_CU_PROVISIONED_RESOURCE_NAME": "string", "_CU_PROVISIONED_PROJECT_NAME": "string",
		"_CU_PROVISIONED_PROJECT_ENDPOINT": "string", "_CU_PROVISIONED_ENDPOINT": "string",
		"_CU_PROVISIONED_MODEL_DEPLOYMENTS": "array",
	}, outputs)
	directory := testutil.TempDir(t)
	_, err = Materialize(t.Context(), directory, choices(), false)
	require.NoError(t, err)
	// #nosec G304 -- Reads only the generated test environment.
	environment, err := os.ReadFile(filepath.Join(directory, ".azure/dev/.env"))
	require.NoError(t, err)
	assert.NotContains(t, string(environment), "_CU_PROVISIONED_", "generation must not invent deployment outputs")
	assert.Contains(t, string(environment), `AZURE_LOCATION="eastus2"`)
	assert.Contains(t, string(environment), `AZURE_SUBSCRIPTION_ID="sub"`)
	assert.Contains(t, string(environment), `AZURE_TENANT_ID="tenant"`)
}

func TestGenerationReuseForceAndPreflight(t *testing.T) {
	directory := testutil.TempDir(t)
	_, err := Materialize(t.Context(), directory, choices(), false)
	require.NoError(t, err)
	write := func(path, body string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(directory, path), []byte(body), 0o600))
	}
	write(".azure/dev/.env", "# user\r\nCUSTOM=\"retained\"\r\n"+
		"CU_MODEL_SETUP_COMPLETE=\"true\"\r\nAZURE_LOCATION=\"old\"\r\n")
	write(".azure/config.json", `{"unknown":{"number":9007199254740993},"version":99}`)
	write("infra/models.json", `[{"name":"retained"}]`)
	write("azure.yaml", "customized")
	result, err := Materialize(t.Context(), directory, choices(), false)
	require.NoError(t, err)
	assert.True(t, result.ReusedExistingTemplate)
	assert.Equal(t, []string{".azure/config.json", ".azure/dev/.env", ".azure/dev/config.json",
		"infra/main.parameters.json"}, result.Files)
	body, err := os.ReadFile(filepath.Join(directory, ".azure/config.json")) // #nosec G304 -- Private test state.
	require.NoError(t, err)
	assert.JSONEq(t, `{"unknown":{"number":9007199254740993},"version":1,"defaultEnvironment":"dev"}`, string(body))
	assert.Contains(t, string(body), "9007199254740993")
	body, err = os.ReadFile(filepath.Join(directory, ".azure/dev/.env")) // #nosec G304 -- Private test state.
	require.NoError(t, err)
	assert.Contains(t, string(body), "CUSTOM=\"retained\"\r\n")
	assert.Contains(t, string(body), "CU_MODEL_SETUP_COMPLETE=\"true\"\r\n")
	assert.Contains(t, string(body), "AZURE_LOCATION=\"eastus2\"\r\n")
	body, err = os.ReadFile(filepath.Join(directory, "infra/models.json")) // #nosec G304 -- Private test state.
	require.NoError(t, err)
	assert.Equal(t, `[{"name":"retained"}]`, string(body))
	write(".azure/config.json", `[]`)
	_, err = Materialize(t.Context(), directory, choices(), false)
	require.Error(t, err)
	body, err = os.ReadFile(filepath.Join(directory, "azure.yaml")) // #nosec G304 -- Private test state.
	require.NoError(t, err)
	assert.Equal(t, "customized", string(body))
	result, err = Materialize(t.Context(), directory, choices(), true)
	require.NoError(t, err)
	assert.False(t, result.ReusedExistingTemplate)
	body, err = os.ReadFile(filepath.Join(directory, "infra/models.json")) // #nosec G304 -- Private test state.
	require.NoError(t, err)
	assert.Equal(t, "[]\n", string(body))
	body, err = os.ReadFile(filepath.Join(directory, ".azure/dev/.env")) // #nosec G304 -- Private test state.
	require.NoError(t, err)
	assert.Contains(t, string(body), "CUSTOM=\"retained\"\r\n")
	assert.Contains(t, string(body), "CU_MODEL_SETUP_COMPLETE=\"true\"\r\n")
}

func TestGenerationInputAndPathSafety(t *testing.T) {
	for _, mutate := range []func(*Choices){
		func(c *Choices) { c.Environment = "../escape" },
		func(c *Choices) { c.Environment = "." },
		func(c *Choices) { c.Location = "invalid" },
		func(c *Choices) { c.FoundryPrefix = "-unsafe" },
		func(c *Choices) { c.FoundryPrefix = strings.Repeat("a", 21) },
		func(c *Choices) {
			c.FoundryPrefix, c.FoundryEndpoint = "prefix", "https://account.services.ai.azure.com"
		},
		func(c *Choices) { c.FoundryEndpoint = "http://account.services.ai.azure.com" },
		func(c *Choices) { c.FoundryEndpoint = "https://account.services.ai.azure.com" },
		func(c *Choices) {
			c.FoundryEndpoint, c.FoundryResourceGroup = "https://account.services.ai.azure.com", "existing-group"
			c.AssignRoles = true
		},
		func(c *Choices) { c.SubscriptionID = "\"$(touch injected)" },
		func(c *Choices) { c.TenantID = "" },
		func(c *Choices) { c.APIVersion = "invalid" },
		func(c *Choices) { c.ModelSelection = "none,model" },
		func(c *Choices) { c.ModelSelection = "model,,other" },
	} {
		c := choices()
		mutate(&c)
		target := filepath.Join(testutil.TempDir(t), "absent")
		_, err := Materialize(t.Context(), target, c, false)
		require.Error(t, err)
		_, err = os.Stat(target)
		assert.True(t, os.IsNotExist(err))
	}
	c := choices()
	c.Environment, c.Location, c.FoundryPrefix = "  DEV ", "East US 2", " New-Resource "
	normalized, err := c.Normalize()
	require.NoError(t, err)
	assert.Equal(t, "dev", normalized.Environment)
	assert.Equal(t, "eastus2", normalized.Location)
	assert.Equal(t, "new-resource", normalized.FoundryPrefix)
	c.FoundryPrefix, c.FoundryEndpoint = "", "https://ACCOUNT.services.ai.azure.com/"
	c.FoundryResourceGroup = "existing-group"
	normalized, err = c.Normalize()
	require.NoError(t, err)
	assert.Equal(t, "https://account.services.ai.azure.com", normalized.FoundryEndpoint)
	target := testutil.TempDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(target, "unrelated.txt"), []byte("keep"), 0o600))
	_, err = Materialize(t.Context(), target, choices(), false)
	require.Error(t, err)
	_, err = os.Stat(filepath.Join(target, "azure.yaml"))
	assert.True(t, os.IsNotExist(err))
	if runtime.GOOS != "windows" {
		other := testutil.TempDir(t)
		require.NoError(t, os.Symlink(other, filepath.Join(target, ".azure")))
		_, err = Materialize(t.Context(), target, choices(), true)
		require.Error(t, err)
		entries, err := os.ReadDir(other)
		require.NoError(t, err)
		assert.Empty(t, entries)
		_, err = os.Stat(filepath.Join(target, "azure.yaml"))
		assert.True(t, os.IsNotExist(err))
	}
}

func TestEnvironmentMergeDuplicateManagedValues(t *testing.T) {
	value := "# custom\nexport AZURE_LOCATION=old\nAZURE_LOCATION=duplicate\nCU_MODEL_SETUP_COMPLETE=true\n" +
		"CU_MODEL_SETUP_COMPLETE=false\nUNKNOWN=unchanged"
	result := MergeEnvironment(value, choices())
	assert.Equal(t, 1, strings.Count(result, "AZURE_LOCATION="))
	assert.Equal(t, 2, strings.Count(result, "CU_MODEL_SETUP_COMPLETE="))
	assert.Contains(t, result, "CU_MODEL_SETUP_COMPLETE=true\n")
	assert.Contains(t, result, "UNKNOWN=unchanged\n")
	assert.Contains(t, result, "CU_ASSIGN_ROLES=\"false\"\n")
	assert.Equal(t, "'a'\"'\"'b'", shellQuote("a'b"))
}

func resolvedResourceParameters(t *testing.T, directory, name string) map[string]string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(directory, ".azure", name, ".env")) // #nosec G304 -- Test project.
	require.NoError(t, err)
	values, err := godotenv.Unmarshal(string(body))
	require.NoError(t, err)
	body, err = os.ReadFile(filepath.Join(directory, "infra/main.parameters.json")) // #nosec G304 -- Test project.
	require.NoError(t, err)
	var document struct {
		Parameters map[string]struct {
			Value string `json:"value"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(body, &document))
	result := map[string]string{}
	for _, binding := range resourceParameters {
		// Use azd's Bicep substitution engine with the selected environment's saved inputs.
		value, err := envsubst.Eval(document.Parameters[binding.name].Value, func(key string) string {
			require.Contains(t, values, key)
			return values[key]
		})
		require.NoError(t, err)
		result[binding.name] = value
	}
	return result
}

func expectedResourceParameters(c Choices) map[string]string {
	return map[string]string{
		"foundryResourcePrefix": c.FoundryPrefix, "existingFoundryEndpoint": c.FoundryEndpoint,
		"existingFoundryResourceGroup": c.FoundryResourceGroup, "assignRolesToPrincipal": strconv.FormatBool(c.AssignRoles),
	}
}

func TestGenerationPersistsEnvironmentScopedResourceInputs(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			directory := testutil.TempDir(t)
			c := choices()
			c.FoundryPrefix, c.AssignRoles, c.ModelSelection = "custom", true, "recommended"
			if existing {
				c.FoundryPrefix, c.AssignRoles = "", false
				c.FoundryEndpoint = "https://adopted.services.ai.azure.com"
				c.FoundryResourceGroup = "adopted-group"
			}
			_, err := Materialize(t.Context(), directory, c, false)
			require.NoError(t, err)
			read := func(path string) []byte {
				t.Helper()
				body, err := os.ReadFile(filepath.Join(directory, path)) // #nosec G304 -- Private test project.
				require.NoError(t, err)
				return body
			}
			env := string(read(".azure/dev/.env"))
			for _, name := range []string{"CU_MODEL_SELECTION", "CU_CONFIG_SETUP_FORCE", "CU_MODEL_SETUP_COMPLETE"} {
				assert.NotContains(t, env, name)
			}
			for _, binding := range resourceParameters {
				assert.Contains(t, env, binding.key+"=")
			}
			var parameters struct {
				Parameters map[string]struct {
					Value json.RawMessage `json:"value"`
				} `json:"parameters"`
			}
			require.NoError(t, json.Unmarshal(read("infra/main.parameters.json"), &parameters))
			for _, binding := range resourceParameters {
				expected, err := json.Marshal("${" + binding.key + "=" + binding.fallback + "}")
				require.NoError(t, err)
				assert.JSONEq(t, string(expected), string(parameters.Parameters[binding.name].Value))
			}
			assert.Equal(t, expectedResourceParameters(c), resolvedResourceParameters(t, directory, "dev"))
			assert.JSONEq(t, `{"cu":{"modelSelection":"recommended","forceConfigurationSetup":false}}`,
				string(read(".azure/dev/config.json")))
		})
	}
}

func TestGenerationPreservesEnvironmentConfigAndRejectsInvalidConfigBeforeWrites(t *testing.T) {
	directory := testutil.TempDir(t)
	_, err := Materialize(t.Context(), directory, choices(), false)
	require.NoError(t, err)
	configPath := filepath.Join(directory, ".azure/dev/config.json")
	const saved = `{"unknown":9007199254740993,"cu":{"modelSetupComplete":true,"modelDeployments":{"gpt":"saved"}}}`
	require.NoError(t, os.WriteFile(configPath, []byte(saved), 0o600))
	c := choices()
	c.ModelSelection = "recommended"
	_, err = Materialize(t.Context(), directory, c, false)
	require.NoError(t, err)
	body, err := os.ReadFile(configPath) // #nosec G304 -- Private test project.
	require.NoError(t, err)
	assert.JSONEq(t, `{"unknown":9007199254740993,"cu":{"modelSetupComplete":true,
		"modelDeployments":{"gpt":"saved"},"modelSelection":"recommended","forceConfigurationSetup":false}}`, string(body))
	_, err = Materialize(t.Context(), directory, c, true)
	require.NoError(t, err)
	body, err = os.ReadFile(configPath) // #nosec G304 -- Private test project.
	require.NoError(t, err)
	assert.Contains(t, string(body), `"modelSetupComplete": true`)
	assert.Contains(t, string(body), `"forceConfigurationSetup": false`)
	for _, bad := range []string{`[]`, `null`, `{"cu":[]}`, `{"cu":null}`, `{"cu":false}`} {
		require.NoError(t, os.WriteFile(configPath, []byte(bad), 0o600))
		parameterPath := filepath.Join(directory, "infra/main.parameters.json")
		before, err := os.ReadFile(parameterPath) // #nosec G304 -- Private test project.
		require.NoError(t, err)
		c.FoundryPrefix = "different"
		_, err = Materialize(t.Context(), directory, c, false)
		require.Error(t, err)
		after, err := os.ReadFile(parameterPath) // #nosec G304 -- Private test project.
		require.NoError(t, err)
		assert.Equal(t, before, after)
	}
}

func TestGenerationMigratesLegacyParametersAndPreservesCustomState(t *testing.T) {
	directory := testutil.TempDir(t)
	_, err := Materialize(t.Context(), directory, choices(), false)
	require.NoError(t, err)
	write := func(path, body string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(directory, path), []byte(body), 0o600))
	}
	const legacy = `{"parameters":{
		"foundryResourcePrefix":{"value":"${FOUNDRY_RESOURCE_PREFIX=}","custom":"kept"},
		"existingFoundryEndpoint":{"value":"${FOUNDRY_EXISTING_ENDPOINT=}"},
		"existingFoundryResourceGroup":{"value":"${FOUNDRY_EXISTING_RESOURCE_GROUP=}"},
		"assignRolesToPrincipal":{"value":"${AZD_ASSIGN_ROLES=false}"},
		"location":{"value":"${AZURE_LOCATION}"},"custom":{"value":9007199254740993}},
		"metadata":{"custom":"kept"}}`
	write("infra/main.parameters.json", legacy)
	write("azure.yaml", "customized")
	write("infra/models.json", `[{"name":"saved"}]`)
	write(".azure/dev/.env", "FOUNDRY_RESOURCE_PREFIX=\"old\"\nCU_MODEL_SELECTION=\"prompt\"\nCUSTOM=\"kept\"\n")
	write(".azure/dev/config.json", `{"cu":{"modelSetupComplete":true},"custom":"kept"}`)
	c := choices()
	c.FoundryPrefix, c.ModelSelection = "new-prefix", "recommended"
	_, err = Materialize(t.Context(), directory, c, false)
	require.NoError(t, err)
	body, err := os.ReadFile(filepath.Join(directory, "infra/main.parameters.json")) // #nosec G304 -- Test project.
	require.NoError(t, err)
	assert.JSONEq(t, `{"parameters":{
		"foundryResourcePrefix":{"value":"${CU_FOUNDRY_RESOURCE_PREFIX=}","custom":"kept"},
		"existingFoundryEndpoint":{"value":"${CU_FOUNDRY_EXISTING_ENDPOINT=}"},
		"existingFoundryResourceGroup":{"value":"${CU_FOUNDRY_EXISTING_RESOURCE_GROUP=}"},
		"assignRolesToPrincipal":{"value":"${CU_ASSIGN_ROLES=false}"},"location":{"value":"${AZURE_LOCATION}"},
		"custom":{"value":9007199254740993}},"metadata":{"custom":"kept"}}`, string(body))
	for path, expected := range map[string]string{
		"azure.yaml": "customized", "infra/models.json": `[{"name":"saved"}]`,
	} {
		body, err := os.ReadFile(filepath.Join(directory, path)) // #nosec G304 -- Private test project.
		require.NoError(t, err)
		assert.Equal(t, expected, string(body))
	}
	body, err = os.ReadFile(filepath.Join(directory, ".azure/dev/.env")) // #nosec G304 -- Private test project.
	require.NoError(t, err)
	assert.Contains(t, string(body), "CU_FOUNDRY_RESOURCE_PREFIX=\"new-prefix\"\n")
	assert.True(t, strings.HasPrefix(string(body), "FOUNDRY_RESOURCE_PREFIX=\"old\"\n"))
	assert.Contains(t, string(body), "CU_MODEL_SELECTION=\"prompt\"\n")
	assert.Contains(t, string(body), "CUSTOM=\"kept\"\n")
	body, err = os.ReadFile(filepath.Join(directory, ".azure/dev/config.json")) // #nosec G304 -- Private test project.
	require.NoError(t, err)
	assert.JSONEq(t, `{"cu":{"modelSetupComplete":true,"modelSelection":"recommended",
		"forceConfigurationSetup":false},"custom":"kept"}`, string(body))
	write("infra/main.parameters.json", `{"parameters":null}`)
	before, err := os.ReadFile(filepath.Join(directory, ".azure/dev/.env")) // #nosec G304 -- Private test project.
	require.NoError(t, err)
	c.Location = "westus3"
	_, err = Materialize(t.Context(), directory, c, false)
	require.Error(t, err)
	after, err := os.ReadFile(filepath.Join(directory, ".azure/dev/.env")) // #nosec G304 -- Private test project.
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestGenerationResourceParametersRemainScopedAcrossEnvironments(t *testing.T) {
	for _, mode := range []string{"existing resources", "new resources", "mixed resources"} {
		for _, reverse := range []bool{false, true} {
			for _, force := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/reverse=%t/force=%t", mode, reverse, force), func(t *testing.T) {
					directory := testutil.TempDir(t)
					dev, prod := choices(), choices()
					dev.FoundryEndpoint, dev.FoundryResourceGroup = "https://dev.services.ai.azure.com", "dev-group"
					prod.Environment = "prod"
					prod.FoundryEndpoint, prod.FoundryResourceGroup = "https://prod.services.ai.azure.com", "prod-group"
					if mode != "existing resources" {
						dev.FoundryEndpoint, dev.FoundryResourceGroup = "", ""
						dev.FoundryPrefix, dev.AssignRoles = "dev-prefix", true
					}
					if mode == "new resources" {
						prod.FoundryEndpoint, prod.FoundryResourceGroup = "", ""
						prod.FoundryPrefix = "prod-prefix"
					}
					first, second := dev, prod
					if reverse {
						first, second = second, first
					}
					for _, binding := range resourceParameters {
						t.Setenv(binding.key, "unrelated-process-value")
					}
					_, err := Materialize(t.Context(), directory, first, false)
					require.NoError(t, err)
					firstPath := filepath.Join(directory, ".azure", first.Environment, ".env")
					firstBody, err := os.ReadFile(firstPath) // #nosec G304 -- Test environment.
					require.NoError(t, err)
					_, err = Materialize(t.Context(), directory, second, force)
					require.NoError(t, err)
					assert.Equal(t, expectedResourceParameters(dev), resolvedResourceParameters(t, directory, "dev"))
					assert.Equal(t, expectedResourceParameters(prod), resolvedResourceParameters(t, directory, "prod"))
					body, err := os.ReadFile(firstPath) // #nosec G304 -- Test environment.
					require.NoError(t, err)
					assert.Equal(t, firstBody, body)
					first.FoundryPrefix, first.AssignRoles = "", false
					first.FoundryEndpoint = "https://replacement.services.ai.azure.com"
					first.FoundryResourceGroup = "replacement-group"
					_, err = Materialize(t.Context(), directory, first, false)
					require.NoError(t, err)
					assert.Equal(t, expectedResourceParameters(first),
						resolvedResourceParameters(t, directory, first.Environment))
					assert.Equal(t, expectedResourceParameters(second),
						resolvedResourceParameters(t, directory, second.Environment))
				})
			}
		}
	}
}

func TestGenerationMigratesLegacyResourceKeysAcrossEnvironments(t *testing.T) {
	for _, force := range []bool{false, true} {
		for _, scenario := range []string{"legacy", "new keys win", "empty new keys win", "missing keys"} {
			t.Run(fmt.Sprintf("%s/force=%t", scenario, force), func(t *testing.T) {
				directory := testutil.TempDir(t)
				_, err := Materialize(t.Context(), directory, choices(), false)
				require.NoError(t, err)
				parameterPath := filepath.Join(directory, "infra/main.parameters.json")
				body, err := os.ReadFile(parameterPath) // #nosec G304 -- Test project.
				require.NoError(t, err)
				for _, binding := range resourceParameters {
					body = []byte(strings.ReplaceAll(string(body), binding.key+"=", binding.legacy+"="))
					t.Setenv(binding.key, "unrelated-process-value")
					t.Setenv(binding.legacy, "unrelated-process-value")
				}
				// #nosec G703 -- Fixed parameter file in a test-owned temporary project.
				require.NoError(t, os.WriteFile(parameterPath, body, 0o600))
				expectedByEnvironment := map[string]map[string]string{}
				const custom = "CUSTOM=\"retained\"\r\n_CU_PROVISIONED_RESOURCE_NAME=\"saved-account\"\r\n"
				for _, name := range []string{"dev", "other"} {
					require.NoError(t, os.MkdirAll(filepath.Join(directory, ".azure", name), 0o700))
					var env strings.Builder
					env.WriteString(custom)
					expected := map[string]string{}
					for _, binding := range resourceParameters {
						value := binding.fallback
						if scenario != "missing keys" {
							value = "old-" + name
							if binding.name == "assignRolesToPrincipal" {
								value = "true"
							}
							env.WriteString(binding.legacy + "=\"" + value + "\"\r\n")
						}
						switch scenario {
						case "new keys win":
							value = "new-" + name
							if binding.name == "assignRolesToPrincipal" {
								value = "false"
							}
							env.WriteString(binding.key + "=\"" + value + "\"\r\n")
						case "empty new keys win":
							value = ""
							env.WriteString(binding.key + "=\"\"\r\n")
						}
						if value == "" {
							value = binding.fallback
						}
						expected[binding.name] = value
					}
					envPath := filepath.Join(directory, ".azure", name, ".env")
					require.NoError(t, os.WriteFile(envPath, []byte(env.String()), 0o600))
					expectedByEnvironment[name] = expected
				}
				prod := choices()
				prod.Environment, prod.FoundryPrefix, prod.AssignRoles = "prod", "selected-prefix", true
				_, err = Materialize(t.Context(), directory, prod, force)
				require.NoError(t, err)
				assert.Equal(t, expectedResourceParameters(prod), resolvedResourceParameters(t, directory, "prod"))
				migrated := map[string][]byte{}
				for _, name := range []string{"dev", "other"} {
					assert.Equal(t, expectedByEnvironment[name], resolvedResourceParameters(t, directory, name))
					envPath := filepath.Join(directory, ".azure", name, ".env")
					body, err := os.ReadFile(envPath) // #nosec G304 -- Test project.
					require.NoError(t, err)
					assert.Contains(t, string(body), custom)
					migrated[name] = body
					if scenario == "empty new keys win" {
						values, err := godotenv.Unmarshal(string(body))
						require.NoError(t, err)
						for _, binding := range resourceParameters {
							assert.Contains(t, values, binding.key)
							assert.Empty(t, values[binding.key])
						}
					}
				}
				body, err = os.ReadFile(filepath.Join(directory, ".azure/prod/.env")) // #nosec G304 -- Test project.
				require.NoError(t, err)
				values, err := godotenv.Unmarshal(string(body))
				require.NoError(t, err)
				for _, binding := range resourceParameters {
					assert.NotContains(t, values, binding.legacy)
				}
				_, err = Materialize(t.Context(), directory, prod, force)
				require.NoError(t, err)
				for name, before := range migrated {
					envPath := filepath.Join(directory, ".azure", name, ".env")
					body, err := os.ReadFile(envPath) // #nosec G304 -- Test project.
					require.NoError(t, err)
					assert.Equal(t, before, body)
				}
			})
		}
	}
}

func TestGenerationMigratesSharedLiteralParametersWithoutRetargetingOtherEnvironments(t *testing.T) {
	for _, mode := range []string{"existing resource", "new resource", "mixed bindings"} {
		for _, force := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/force=%t", mode, force), func(t *testing.T) {
				directory := testutil.TempDir(t)
				dev := choices()
				dev.FoundryEndpoint, dev.FoundryResourceGroup = "https://dev.services.ai.azure.com", "dev-group"
				if mode != "existing resource" {
					dev.FoundryEndpoint, dev.FoundryResourceGroup = "", ""
					dev.FoundryPrefix, dev.AssignRoles = "dev-prefix", true
				}
				_, err := Materialize(t.Context(), directory, dev, false)
				require.NoError(t, err)
				parameterPath := filepath.Join(directory, "infra/main.parameters.json")
				body, err := os.ReadFile(parameterPath) // #nosec G304 -- Test parameters.
				require.NoError(t, err)
				parameters, err := jsonObject(body)
				require.NoError(t, err)
				var document map[string]map[string]string
				require.NoError(t, json.Unmarshal(parameters["parameters"], &document))
				for name, value := range expectedResourceParameters(dev) {
					if mode != "mixed bindings" || name == "foundryResourcePrefix" {
						document[name]["value"] = value
					}
				}
				parameters["parameters"], err = json.Marshal(document)
				require.NoError(t, err)
				body, err = json.Marshal(parameters)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(parameterPath, body, 0o600))
				stale := dev
				stale.Environment, stale.FoundryPrefix = "test", "stale-prefix"
				require.NoError(t, os.MkdirAll(filepath.Join(directory, ".azure/test"), 0o700))
				const custom = "CUSTOM=\"retained\"\r\n_CU_PROVISIONED_RESOURCE_NAME=\"saved-account\"\r\n"
				require.NoError(t, os.WriteFile(filepath.Join(directory, ".azure/test/.env"),
					[]byte(MergeEnvironment(custom, stale)), 0o600))
				before := resolvedResourceParameters(t, directory, "test")
				configPath := filepath.Join(directory, ".azure/dev/config.json")
				config, err := os.ReadFile(configPath) // #nosec G304 -- Test config.
				require.NoError(t, err)
				prod := choices()
				prod.Environment = "prod"
				prod.FoundryEndpoint, prod.FoundryResourceGroup = "https://prod.services.ai.azure.com", "prod-group"
				_, err = Materialize(t.Context(), directory, prod, force)
				require.NoError(t, err)
				assert.Equal(t, expectedResourceParameters(dev), resolvedResourceParameters(t, directory, "dev"))
				assert.Equal(t, before, resolvedResourceParameters(t, directory, "test"))
				assert.Equal(t, expectedResourceParameters(prod), resolvedResourceParameters(t, directory, "prod"))
				body, err = os.ReadFile(filepath.Join(directory, ".azure/test/.env")) // #nosec G304 -- Test state.
				require.NoError(t, err)
				assert.Contains(t, string(body), custom)
				body, err = os.ReadFile(configPath) // #nosec G304 -- Test config.
				require.NoError(t, err)
				assert.Equal(t, config, body)
			})
		}
	}
}

func TestGenerationConfigRemainsScopedToSelectedEnvironment(t *testing.T) {
	directory := testutil.TempDir(t)
	c := choices()
	_, err := Materialize(t.Context(), directory, c, false)
	require.NoError(t, err)
	first, err := os.ReadFile(filepath.Join(directory, ".azure/dev/config.json")) // #nosec G304 -- Test project.
	require.NoError(t, err)
	c.Environment, c.ModelSelection, c.ForceConfigurationSetup = "other", "recommended", true
	_, err = Materialize(t.Context(), directory, c, false)
	require.NoError(t, err)
	body, err := os.ReadFile(filepath.Join(directory, ".azure/other/config.json")) // #nosec G304 -- Test project.
	require.NoError(t, err)
	assert.JSONEq(t, `{"cu":{"modelSelection":"recommended","forceConfigurationSetup":true}}`, string(body))
	body, err = os.ReadFile(filepath.Join(directory, ".azure/dev/config.json")) // #nosec G304 -- Test project.
	require.NoError(t, err)
	assert.Equal(t, first, body)
}

func TestGenerationMigrationRejectsUnsafeStateBeforeWrites(t *testing.T) {
	for _, scenario := range []string{
		"null parameter", "boolean parameter", "custom substitution", "unsafe literal",
		"environment symlink", "environment file symlink", "nonregular environment file", "invalid selected config",
		"invalid saved environment", "unsafe saved input",
	} {
		for _, force := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/force=%t", scenario, force), func(t *testing.T) {
				directory := testutil.TempDir(t)
				_, err := Materialize(t.Context(), directory, choices(), false)
				require.NoError(t, err)
				read := func(path string) []byte {
					t.Helper()
					body, err := os.ReadFile(filepath.Join(directory, path)) // #nosec G304 -- Test-owned state.
					require.NoError(t, err)
					return body
				}
				body := strings.ReplaceAll(string(read("infra/main.parameters.json")),
					`"${CU_FOUNDRY_RESOURCE_PREFIX=}"`, `"old-prefix"`)
				bad := map[string]string{
					"null parameter": "null", "boolean parameter": "true",
					"custom substitution": `"${CUSTOM_PREFIX}"`, "unsafe literal": `"bad\nprefix"`,
				}
				if value, exists := bad[scenario]; exists {
					body = strings.ReplaceAll(body, `"old-prefix"`, value)
				}
				require.NoError(t, os.WriteFile(filepath.Join(directory, "infra/main.parameters.json"), []byte(body), 0o600))
				switch scenario {
				case "invalid saved environment", "unsafe saved input":
					require.NoError(t, os.MkdirAll(filepath.Join(directory, ".azure/other"), 0o700))
					env := "FOUNDRY_EXISTING_ENDPOINT=\"unterminated\n"
					if scenario == "unsafe saved input" {
						env = "FOUNDRY_EXISTING_ENDPOINT=\"bad\\nendpoint\"\n"
					}
					require.NoError(t, os.WriteFile(filepath.Join(directory, ".azure/other/.env"), []byte(env), 0o600))
				case "environment symlink", "environment file symlink":
					if runtime.GOOS == "windows" {
						t.Skip("symlink permission is not guaranteed")
					}
					outside := testutil.TempDir(t)
					path := filepath.Join(directory, ".azure/other")
					if scenario == "environment file symlink" {
						require.NoError(t, os.Mkdir(path, 0o700))
						path = filepath.Join(path, ".env")
						outside = filepath.Join(outside, "state")
						require.NoError(t, os.WriteFile(outside, []byte("CUSTOM=outside\n"), 0o600))
					}
					require.NoError(t, os.Symlink(outside, path))
				case "nonregular environment file":
					require.NoError(t, os.MkdirAll(filepath.Join(directory, ".azure/other/.env"), 0o700))
				case "invalid selected config":
					require.NoError(t, os.MkdirAll(filepath.Join(directory, ".azure/prod"), 0o700))
					require.NoError(t, os.WriteFile(filepath.Join(directory, ".azure/prod/config.json"),
						[]byte("null"), 0o600))
				}
				before := map[string][]byte{}
				for _, path := range []string{"infra/main.parameters.json", ".azure/config.json",
					".azure/dev/.env", ".azure/dev/config.json", "azure.yaml", "infra/models.json"} {
					before[path] = read(path)
				}
				prod := choices()
				prod.Environment, prod.FoundryPrefix = "prod", "new-prefix"
				_, err = Materialize(t.Context(), directory, prod, force)
				require.Error(t, err)
				for path, expected := range before {
					assert.Equal(t, expected, read(path), path)
				}
				assert.NoFileExists(t, filepath.Join(directory, ".azure/prod/.env"))
			})
		}
	}
}

func TestGeneratedShellLaunchers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process stub")
	}
	for _, shell := range []string{"sh", "pwsh"} {
		t.Run(shell, func(t *testing.T) {
			executable, err := exec.LookPath(shell)
			if err != nil {
				t.Skip("shell not installed")
			}
			for _, test := range []struct {
				name, exitCode, environment string
			}{
				{"success", "0", "prod"}, {"failure", "7", "prod"}, {"missing azd", "missing", "prod"},
				{"missing environment", "0", ""}, {"empty environment", "0", ""},
			} {
				t.Run(test.name, func(t *testing.T) {
					root := testutil.TempDir(t)
					project := filepath.Join(root, "project")
					_, err := Materialize(t.Context(), project, choices(), false)
					require.NoError(t, err)
					bin := filepath.Join(root, "bin")
					require.NoError(t, os.Mkdir(bin, 0o700))
					log := filepath.Join(root, "calls")
					if test.exitCode != "missing" {
						stub := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellQuote(log) + "\nexit " + test.exitCode + "\n"
						stubPath := filepath.Join(bin, "azd")
						require.NoError(t, os.WriteFile(stubPath, []byte(stub), 0o600))
						require.NoError(t, os.Chmod(stubPath, 0o700)) // #nosec G302 -- Executable test-owned CLI stub.
					}
					args := []string{filepath.Join(project, "hooks/postprovision.sh")}
					if shell == "pwsh" {
						args = []string{"-NoLogo", "-NoProfile", "-File", filepath.Join(project, "hooks/postprovision.ps1")}
					}
					command := exec.CommandContext(t.Context(), executable, args...) // #nosec G204 -- Test-owned script.
					command.Env = []string{"PATH=" + bin, "HOME=" + root, "AZD_ENVIRONMENT=dev"}
					if test.name != "missing environment" {
						command.Env = append(command.Env, "AZURE_ENV_NAME="+test.environment)
					}
					body, err := command.CombinedOutput()
					if test.environment == "" {
						require.Error(t, err)
						assert.Equal(t, 1, command.ProcessState.ExitCode(), string(body))
						assert.Contains(t, string(body), "Postprovision requires AZURE_ENV_NAME")
						assert.NoFileExists(t, log, "a missing hook environment must not fall back to dev")
						return
					}
					switch test.exitCode {
					case "0":
						require.NoError(t, err, string(body))
					case "7":
						require.Error(t, err)
						assert.Equal(t, 7, command.ProcessState.ExitCode(), string(body))
					default:
						require.Error(t, err)
						assert.Contains(t, string(body), "azd")
						assert.NoFileExists(t, log)
						return
					}
					body, err = os.ReadFile(log) // #nosec G304 -- Test-owned invocation log.
					require.NoError(t, err)
					assert.Equal(t, "ai\ncu\ninfra\n_postprovision-v1\n--environment\nprod\n", string(body))
				})
			}
		})
	}
}
