// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const hostingClusterID = "/subscriptions/cluster-sub/resourceGroups/aks-rg/providers/" +
	"Microsoft.ContainerService/managedClusters/aks"
const hostingVNetID = "/subscriptions/network-sub/resourceGroups/network-rg/providers/Microsoft.Network/virtualNetworks/vnet"
const hostingStorageID = "/subscriptions/resources-sub/resourceGroups/data-rg/providers/" +
	"Microsoft.Storage/storageAccounts/data"
const hostingIdentityID = "/subscriptions/resources-sub/resourceGroups/identity-rg/providers/" +
	"Microsoft.ManagedIdentity/userAssignedIdentities/"

func hostingYAML() string {
	return `services:
  project:
    host: azure.ai.project
    agentHosting:
      clusterResourceId: ` + hostingClusterID + `
    network:
      agentSubnet: {vnet: ` + hostingVNetID + `, name: agents}
      peSubnet: {vnet: ` + hostingVNetID + `, name: endpoints}
    monitoring:
      enabled: false
`
}

func TestHostingDefaultSynthesis(t *testing.T) {
	for _, preserve := range []bool{false, true} {
		result, err := Synthesize(Input{
			RawAzureYAML: []byte(strings.Split(hostingYAML(), "    network:")[0]),
			ServiceName:  "project", PreserveVarRefs: preserve,
		})
		require.NoError(t, err)
		require.NotNil(t, result.Hosting)
		assert.Equal(t, hostingClusterID, result.Hosting.ClusterResourceID)
		assert.Equal(t, false, result.Parameters["includeAcr"], "project-only hosting does not need a registry")
		assert.Equal(t, NetworkModeNone, result.NetworkMode)
		hosting, ok := result.Parameters["agentHosting"].(agentHostingParameter)
		require.True(t, ok)
		assert.Equal(t, "ManagedCluster", hosting.HostingType)
		assert.Equal(t, "default", hosting.Name)
		assert.Equal(t, hostingClusterID, hosting.ClusterResourceID)
		assert.NotContains(t, result.Parameters, "containerRegistryResourceId")
	}
}

func TestHostingAllowsAutoCreatedAcrWithPrivateNetworking(t *testing.T) {
	for _, test := range []struct{ name, raw, mode string }{
		{"byo egress", hostingYAML(), NetworkModeByo},
		{"managed egress", strings.Replace(hostingYAML(),
			"      agentSubnet: {vnet: "+hostingVNetID+", name: agents}\n", "", 1), NetworkModeManaged},
	} {
		for _, preserve := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/preserve=%t", test.name, preserve), func(t *testing.T) {
				raw := test.raw + `  assistant:
    host: azure.ai.agent
    kind: hosted
`
				result, err := Synthesize(Input{
					RawAzureYAML: []byte(raw),
					ServiceName:  "project", PreserveVarRefs: preserve,
				})
				require.NoError(t, err)
				require.NotNil(t, result.Hosting)
				assert.Equal(t, hostingClusterID, result.Hosting.ClusterResourceID)
				assert.Equal(t, true, result.Parameters["includeAcr"])
				assert.Equal(t, true, result.Parameters["enableNetworkIsolation"])
				assert.Equal(t, test.mode, result.NetworkMode)
			})
		}
	}
}

func TestHostingRejectsUnsupportedSelections(t *testing.T) {
	for _, test := range []struct{ field, value, expected string }{
		{"storageAccountResourceId", "[]", "expected a nonempty resource ID"},
		{"hostingManagementIdentityResourceId", "[]", "expected a nonempty resource ID"},
		{"workloadIdentityResourceId", "{}", "expected a nonempty resource ID"},
		{"registryResourceId", "/subscriptions/s/resourceGroups/r/providers/Microsoft.ContainerRegistry/registries/r",
			"unsupported property"},
		{"unknown", "null", "unsupported property"},
	} {
		t.Run(test.field, func(t *testing.T) {
			raw := strings.Replace(hostingYAML(), "    network:",
				"      "+test.field+": "+test.value+"\n    network:", 1)
			result, err := Synthesize(Input{RawAzureYAML: []byte(raw), ServiceName: "project"})
			require.ErrorContains(t, err, test.expected)
			assert.Nil(t, result)
		})
	}
	for _, value := range []string{"null", "[]"} {
		raw := strings.Replace(hostingYAML(), "agentHosting:\n      clusterResourceId: "+hostingClusterID,
			"agentHosting: "+value, 1)
		_, err := Synthesize(Input{RawAzureYAML: []byte(raw), ServiceName: "project"})
		require.ErrorContains(t, err, "expected an object")
	}
}

func TestHostingValidatesClusterAndNetwork(t *testing.T) {
	for _, test := range []struct{ name, raw, expected string }{
		{"empty cluster", strings.Replace(hostingYAML(), hostingClusterID, `""`, 1), "expected a nonempty resource ID"},
		{"wrong type", strings.Replace(hostingYAML(), hostingClusterID, hostingVNetID, 1), "managedClusters resource ID"},
		{"credential URL", strings.Replace(hostingYAML(), hostingClusterID,
			"https://username:secret@host.test/cluster?sig=private#fragment", 1), "managedClusters resource ID"},
		{"same subnet", strings.Replace(hostingYAML(), "name: endpoints", "name: agents", 1), "must differ"},
		{"different VNets", strings.Replace(hostingYAML(), "peSubnet: {vnet: "+hostingVNetID,
			"peSubnet: {vnet: "+hostingVNetID+"-other", 1), "same virtual network"},
		{"existing account", strings.Replace(hostingYAML(), "    agentHosting:",
			"    endpoint: https://existing.services.ai.azure.com/api/projects/project\n    agentHosting:", 1),
			"only supported when azd creates a new Foundry account"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Synthesize(Input{RawAzureYAML: []byte(test.raw), ServiceName: "project"})
			require.ErrorContains(t, err, test.expected)
			if strings.Contains(test.name, "credential") {
				assert.NotContains(t, err.Error(), "secret")
				assert.NotContains(t, err.Error(), "sig=")
			}
		})
	}
}

func TestHostingFileReferencesAndEnvironmentResolution(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "project.yaml")
	config := strings.Replace(strings.TrimPrefix(hostingYAML(), "services:\n  project:\n"),
		hostingClusterID, "${CLUSTER_ID}", 1)
	require.NoError(t, os.WriteFile(path, []byte(config), 0o600))
	input := Input{
		RawAzureYAML: []byte("services:\n  project:\n    $ref: ./project.yaml\n"), ServiceName: "project",
		ProjectRoot: root, Env: map[string]string{"CLUSTER_ID": hostingClusterID},
	}
	result, err := Synthesize(input)
	require.NoError(t, err)
	assert.Equal(t, hostingClusterID, result.Hosting.ClusterResourceID)
	input.PreserveVarRefs = true
	result, err = Synthesize(input)
	require.NoError(t, err)
	assert.Equal(t, "${CLUSTER_ID}", result.Hosting.ClusterResourceID)
	config = strings.Replace(config, "    network:", "      hostingManagementIdentityResourceId: ''\n    network:", 1)
	require.NoError(t, os.WriteFile(path, []byte(config), 0o600))
	result, err = Synthesize(input)
	require.NoError(t, err)
	assert.Empty(t, result.Hosting.HostingManagementIdentityResourceID)
}

func TestHostingSchemaValidatesResourceSelections(t *testing.T) {
	raw, err := os.ReadFile(schemaPath)
	require.NoError(t, err)
	var schemaDocument any
	require.NoError(t, json.Unmarshal(raw, &schemaDocument))
	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource("hosting.json", schemaDocument))
	schema, err := compiler.Compile("hosting.json")
	require.NoError(t, err)
	hosting := map[string]any{"clusterResourceId": hostingClusterID}
	service := map[string]any{"agentHosting": hosting}
	require.NoError(t, schema.Validate(service))
	for field, id := range map[string]string{
		"storageAccountResourceId":            hostingStorageID,
		"hostingManagementIdentityResourceId": hostingIdentityID + "hosting",
		"workloadIdentityResourceId":          hostingIdentityID + "workload",
	} {
		for _, value := range []any{id, "", nil} {
			hosting[field] = value
			require.NoError(t, schema.Validate(service))
		}
		for _, invalid := range []any{1, []any{}} {
			hosting[field] = invalid
			require.Error(t, schema.Validate(service))
		}
		hosting[field] = id
	}
	hosting["containerRegistryResourceId"] = "unsupported"
	require.Error(t, schema.Validate(service))
	delete(hosting, "containerRegistryResourceId")
	for _, endpoint := range []string{"", "${EMPTY}"} {
		service["endpoint"] = endpoint
		require.NoError(t, schema.Validate(service))
		raw := strings.Replace(hostingYAML(), "    agentHosting:", "    endpoint: \""+endpoint+"\"\n    agentHosting:", 1)
		_, err := Synthesize(Input{RawAzureYAML: []byte(raw), ServiceName: "project", Env: map[string]string{"EMPTY": ""}})
		require.NoError(t, err)
	}
	service["endpoint"] = "https://existing.services.ai.azure.com/api/projects/project"
	require.Error(t, schema.Validate(service))
}

func TestHostingEmptySupportingSelectionsRetainDefaultCompatibility(t *testing.T) {
	for _, field := range []string{
		"storageAccountResourceId", "hostingManagementIdentityResourceId", "workloadIdentityResourceId",
	} {
		for _, value := range []string{`""`, "null"} {
			raw := strings.Replace(hostingYAML(), "    network:",
				"      "+field+": "+value+"\n    network:", 1)
			result, err := Synthesize(Input{RawAzureYAML: []byte(raw), ServiceName: "project"})
			require.NoError(t, err)
			assert.Empty(t, result.Hosting.StorageAccountResourceID)
			assert.Empty(t, result.Hosting.HostingManagementIdentityResourceID)
			assert.Empty(t, result.Hosting.WorkloadIdentityResourceID)
			parameter, ok := result.Parameters["agentHosting"].(agentHostingParameter)
			require.True(t, ok)
			assert.Empty(t, parameter.StorageAccountResourceID)
			assert.Empty(t, parameter.HostingManagementIdentityResourceID)
			assert.Empty(t, parameter.WorkloadIdentityResourceID)
		}
	}
}

func TestHostingGraphUsesOneNativeRegistry(t *testing.T) {
	raw, err := ARMTemplate()
	require.NoError(t, err)
	var template any
	require.NoError(t, json.Unmarshal(raw, &template))
	var registries, identities, storages, connections, roleTemplates, clusterGrantModules int
	var visit func(any)
	visit = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			// Reuse-only branches are not deployed on the native registry path.
			if condition, ok := value["condition"].(string); ok &&
				strings.Contains(condition, "not(empty(parameters('existingAcrResourceId')))") {
				return
			}
			switch value["type"] {
			case "Microsoft.Resources/deployments":
				loop, _ := value["copy"].(map[string]any)
				if loop["name"] == "clusterGrants" {
					clusterGrantModules++
					assert.Equal(t, "[format('byo-cluster-{0}-{1}', "+
						"uniqueString(resourceGroup().id, parameters('hostingIdentityName')), "+
						"createArray(variables('readerRoleId'), "+
						"variables('kubernetesExtensionContributorRoleId'))[copyIndex()])]", value["name"])
				}
			case "Microsoft.ContainerRegistry/registries":
				registries++
				assert.Equal(t, "2023-07-01", value["apiVersion"])
				assert.Equal(t, "[parameters('name')]", value["name"])
				sku, ok := value["sku"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "Premium", sku["name"])
				properties, ok := value["properties"].(map[string]any)
				require.True(t, ok)
				assert.NotContains(t, properties, "roleAssignmentMode")
			case "Microsoft.ManagedIdentity/userAssignedIdentities":
				identities++
			case "Microsoft.Storage/storageAccounts":
				storages++
				properties, ok := value["properties"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, false, properties["allowSharedKeyAccess"])
				assert.Equal(t, false, properties["allowBlobPublicAccess"])
				assert.Equal(t, true, properties["supportsHttpsTrafficOnly"])
				assert.Equal(t, "TLS1_2", properties["minimumTlsVersion"])
			case "Microsoft.CognitiveServices/accounts/projects/connections":
				properties, ok := value["properties"].(map[string]any)
				require.True(t, ok)
				// The monitoring graph adds its own AppInsights connection.
				if properties["category"] == "AppInsights" {
					break
				}
				connections++
				assert.Equal(t, "ContainerRegistry", properties["category"])
				assert.Equal(t, "ManagedIdentity", properties["authType"])
			case "Microsoft.Authorization/roleAssignments":
				roleTemplates++
			}
			for _, child := range value {
				visit(child)
			}
		case []any:
			for _, child := range value {
				visit(child)
			}
		}
	}
	visit(template)
	assert.Equal(t, 1, registries, "prerequisites must not pre-create a second/custom registry")
	assert.Equal(t, 2, identities)
	assert.Equal(t, 1, storages)
	assert.Equal(t, 1, connections)
	assert.Equal(t, 5, roleTemplates, "the cluster role template is instantiated for both AKS roles")
	assert.Equal(t, 1, clusterGrantModules)
	text := string(raw)
	for _, required := range []string{
		"agentHostingConfigurations", "ManagedCluster", "hostingManagementIdentityResourceId", "workloadIdentityResourceId",
		"kubeletObjectId", "ContainerRegistry",
		"allowSharedKeyAccess", "allowBlobPublicAccess", "federatedIdentityRoleDefinitionId",
		"acdd72a7-3385-48ef-bd42-f606fba81ae7", "85cb6faf-e071-4c9b-8136-154b5a04f717",
		"ba92f5b4-2d11-453d-a403-e96b0029c9fe", "7f951dda-4ed3-4680-a7ca-43fe172d538d",
	} {
		assert.Contains(t, text, required)
	}
}

func TestHostingGraphBindsDefaultsBeforeAccountCreation(t *testing.T) {
	raw, err := ARMTemplate()
	require.NoError(t, err)
	var graph struct {
		Parameters map[string]json.RawMessage
		Outputs    map[string]json.RawMessage
		Resources  map[string]json.RawMessage
	}
	require.NoError(t, json.Unmarshal(raw, &graph))
	require.Contains(t, graph.Resources, "resources")
	var foundryModule struct {
		Properties struct {
			Template struct {
				Variables map[string]json.RawMessage
				Resources map[string]struct {
					APIVersion string
					Condition  string
					DependsOn  []string
					Properties json.RawMessage
				}
			}
		}
	}
	require.NoError(t, json.Unmarshal(graph.Resources["resources"], &foundryModule))
	foundry := foundryModule.Properties.Template
	require.Contains(t, foundry.Resources, "prerequisites")
	assert.Equal(t, "[parameters('agentHosting').enabled]", foundry.Resources["prerequisites"].Condition)
	for name, expected := range map[string]string{
		"storageAccountName":   "[format('st{0}', variables('resourceToken'))]",
		"hostingIdentityName":  "[format('{0}-hosting-mi', variables('foundryAccountName'))]",
		"workloadIdentityName": "[format('{0}-workload-mi', variables('foundryAccountName'))]",
	} {
		require.Contains(t, foundry.Variables, name)
		var actual string
		require.NoError(t, json.Unmarshal(foundry.Variables[name], &actual))
		assert.Equal(t, expected, actual)
	}
	require.Contains(t, foundry.Resources, "foundryAccount")
	nativeAccount := foundry.Resources["foundryAccount"]
	assert.NotContains(t, nativeAccount.DependsOn, "prerequisites")
	assert.Contains(t, nativeAccount.DependsOn, "network")
	var nativeProperties string
	require.NoError(t, json.Unmarshal(nativeAccount.Properties, &nativeProperties))
	assert.Equal(t, "[variables('foundryAccountProperties')]", nativeProperties)
	require.Contains(t, foundry.Variables, "foundryAccountProperties")
	var commonProperties map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(foundry.Variables["foundryAccountProperties"], &commonProperties))
	assert.NotContains(t, commonProperties, "agentHostingConfigurations")
	for _, branch := range []struct {
		name       string
		apiVersion string
		condition  string
	}{
		{"foundryAccount", "2025-06-01", "[not(parameters('agentHosting').enabled)]"},
		{"byoFoundryAccount", "2026-07-15-preview", "[parameters('agentHosting').enabled]"},
	} {
		for _, suffix := range []string{"", "::project", "::modelDeployments"} {
			name := branch.name + suffix
			require.Contains(t, foundry.Resources, name)
			resource := foundry.Resources[name]
			assert.Equal(t, branch.apiVersion, resource.APIVersion, name)
			assert.Equal(t, branch.condition, resource.Condition, name)
		}
		assert.Contains(t, foundry.Resources[branch.name+"::project"].DependsOn, branch.name+"::modelDeployments")
		for _, dependent := range []string{"foundryManagedNetwork", "privateEndpointDns"} {
			assert.Contains(t, foundry.Resources[dependent].DependsOn, branch.name)
		}
		assert.Contains(t, foundry.Resources["acr"].DependsOn, branch.name+"::project")
	}
	account := foundry.Resources["byoFoundryAccount"]
	assert.Contains(t, account.DependsOn, "prerequisites")
	assert.Contains(t, account.DependsOn, "network")
	var properties string
	require.NoError(t, json.Unmarshal(account.Properties, &properties))
	for _, binding := range []string{
		"variables('hostingIdentityResourceId')",
		"variables('workloadIdentityResourceId')",
		"variables('storageAccountResourceId')",
	} {
		assert.Contains(t, properties, binding, "account preflight must receive concrete IDs, not module output references")
	}
	assert.Contains(t, properties, "variables('foundryAccountProperties')")
	assert.Contains(t, properties, "createObject('agentHostingConfigurations'")
	for _, output := range []string{
		"AZURE_AI_PROJECT_ID", "FOUNDRY_PROJECT_ENDPOINT", "AZURE_CONTAINER_REGISTRY_RESOURCE_ID",
		"AZURE_CONTAINER_REGISTRY_ENDPOINT", "AZURE_AI_PROJECT_ACR_CONNECTION_NAME",
	} {
		assert.Contains(t, graph.Outputs, output)
	}
	assert.Contains(t, graph.Parameters, "kubeletObjectId")
	assert.Contains(t, graph.Parameters, "federatedIdentityRoleDefinitionId")
	assert.Contains(t, graph.Parameters, "hostingPrincipalId")
	assert.Contains(t, graph.Parameters, "workloadPrincipalId")
}

func TestHostingSupportingSelectionsAreIndependent(t *testing.T) {
	for mask := range 8 {
		t.Run(fmt.Sprint(mask), func(t *testing.T) {
			var raw strings.Builder
			raw.WriteString("services:\n  project:\n    host: azure.ai.project\n    agentHosting:\n" +
				"      clusterResourceId: " + hostingClusterID + "\n")
			expected := AgentHostingInput{ClusterResourceID: hostingClusterID}
			for i, selection := range []struct {
				field, value string
				output       *string
			}{
				{"storageAccountResourceId", hostingStorageID, &expected.StorageAccountResourceID},
				{"hostingManagementIdentityResourceId", hostingIdentityID + "hosting",
					&expected.HostingManagementIdentityResourceID},
				{"workloadIdentityResourceId", hostingIdentityID + "workload", &expected.WorkloadIdentityResourceID},
			} {
				if mask&(1<<i) != 0 {
					raw.WriteString("      " + selection.field + ": " + selection.value + "\n")
					*selection.output = selection.value
				}
			}
			result, err := Synthesize(Input{RawAzureYAML: []byte(raw.String()), ServiceName: "project"})
			require.NoError(t, err)
			assert.Equal(t, expected, *result.Hosting)
			assert.Equal(t, false, result.Parameters["includeAcr"])
			parameter, ok := result.Parameters["agentHosting"].(agentHostingParameter)
			require.True(t, ok)
			assert.Equal(t, expected.StorageAccountResourceID, parameter.StorageAccountResourceID)
			assert.Equal(t, expected.HostingManagementIdentityResourceID, parameter.HostingManagementIdentityResourceID)
			assert.Equal(t, expected.WorkloadIdentityResourceID, parameter.WorkloadIdentityResourceID)
		})
	}
}

func TestHostingSupportingReferencesValidateLikeInline(t *testing.T) {
	for _, field := range []string{
		"storageAccountResourceId", "hostingManagementIdentityResourceId", "workloadIdentityResourceId",
	} {
		for _, reference := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/ref=%t", field, reference), func(t *testing.T) {
				root := t.TempDir()
				config := "host: azure.ai.project\nagentHosting:\n  clusterResourceId: " + hostingClusterID +
					"\n  " + field + ": ${SUPPLIED_ID}\n"
				value := hostingIdentityID + "identity"
				if field == "storageAccountResourceId" {
					value = hostingStorageID
				}
				raw := "services:\n  project:\n    " + strings.ReplaceAll(config, "\n", "\n    ")
				if reference {
					require.NoError(t, os.WriteFile(filepath.Join(root, "project.yaml"), []byte(config), 0o600))
					raw = "services:\n  project:\n    $ref: project.yaml\n"
				}
				input := Input{RawAzureYAML: []byte(raw), ServiceName: "project", ProjectRoot: root,
					Env: map[string]string{"SUPPLIED_ID": value}}
				result, err := Synthesize(input)
				require.NoError(t, err)
				encoded, err := json.Marshal(result.Parameters["agentHosting"])
				require.NoError(t, err)
				assert.Contains(t, string(encoded), value)
				input.PreserveVarRefs = true
				result, err = Synthesize(input)
				require.NoError(t, err)
				encoded, err = json.Marshal(result.Parameters["agentHosting"])
				require.NoError(t, err)
				assert.Contains(t, string(encoded), "${SUPPLIED_ID}")
				input.PreserveVarRefs = false
				for _, invalid := range []string{"", hostingClusterID, "https://user:private@host/path?sig=private"} {
					input.Env["SUPPLIED_ID"] = invalid
					result, err := Synthesize(input)
					if invalid == "" {
						require.NoError(t, err)
						encoded, err := json.Marshal(result.Parameters["agentHosting"])
						require.NoError(t, err)
						assert.Contains(t, string(encoded), `"`+field+`":""`)
						continue
					}
					require.Error(t, err)
					assert.Contains(t, err.Error(), field)
					assert.NotContains(t, err.Error(), "private")
				}
			})
		}
	}
	raw := strings.Replace(hostingYAML(), "    network:",
		"      hostingManagementIdentityResourceId: "+hostingIdentityID+"same\n"+
			"      workloadIdentityResourceId: "+strings.ToUpper(hostingIdentityID+"same")+"\n    network:", 1)
	_, err := Synthesize(Input{RawAzureYAML: []byte(raw), ServiceName: "project"})
	require.NoError(t, err)
}

func TestHostingGraphIndependentlyReferencesSuppliedResources(t *testing.T) {
	raw, err := ARMTemplate()
	require.NoError(t, err)
	var top struct {
		Resources map[string]struct {
			Properties struct{ Template json.RawMessage }
		}
	}
	require.NoError(t, json.Unmarshal(raw, &top))
	resourcesRaw := top.Resources["resources"].Properties.Template
	var resources struct {
		Variables map[string]json.RawMessage
		Resources map[string]json.RawMessage
	}
	require.NoError(t, json.Unmarshal(resourcesRaw, &resources))
	for name, selector := range map[string]string{
		"hostingIdentityResourceId":  "suppliedHostingIdentityResourceId",
		"workloadIdentityResourceId": "suppliedWorkloadIdentityResourceId",
		"storageAccountResourceId":   "suppliedStorageAccountResourceId",
	} {
		var expression string
		require.NoError(t, json.Unmarshal(resources.Variables[name], &expression))
		assert.Contains(t, expression, "empty(variables('"+selector+"'))")
		assert.Contains(t, expression, "resourceId(")
		assert.Contains(t, expression, "variables('"+selector+"')")
	}
	var prerequisites struct {
		Resources []struct {
			Name           string
			Condition      string
			SubscriptionID string
			ResourceGroup  string
			Copy           struct{ Name string }
		}
	}
	var deployment struct {
		Properties struct{ Template json.RawMessage }
	}
	require.NoError(t, json.Unmarshal(resources.Resources["prerequisites"], &deployment))
	require.NoError(t, json.Unmarshal(deployment.Properties.Template, &prerequisites))
	const federationGrantName = "[if(empty(parameters('workloadIdentityResourceId')), 'byo-federation-access', " +
		"format('byo-federation-access-{0}', uniqueString(resourceGroup().id, parameters('hostingIdentityName'))))]"
	const storageGrantName = "[if(empty(parameters('storageAccountResourceId')), 'byo-storage-access', " +
		"format('byo-storage-access-{0}', uniqueString(resourceGroup().id, parameters('workloadIdentityName'))))]"
	expected := map[string]string{
		"[parameters('hostingIdentityName')]":  "[empty(parameters('hostingIdentityResourceId'))]",
		"[parameters('workloadIdentityName')]": "[empty(parameters('workloadIdentityResourceId'))]",
		"[parameters('storageAccountName')]":   "[empty(parameters('storageAccountResourceId'))]",
		"clusterGrants":                        "[empty(parameters('hostingIdentityResourceId'))]",
		federationGrantName: "[or(empty(parameters('hostingIdentityResourceId')), " +
			"empty(parameters('workloadIdentityResourceId')))]",
		storageGrantName: "[or(empty(parameters('workloadIdentityResourceId')), " +
			"empty(parameters('storageAccountResourceId')))]",
	}
	require.Len(t, prerequisites.Resources, len(expected))
	for _, resource := range prerequisites.Resources {
		name := resource.Name
		if resource.Copy.Name != "" {
			name = resource.Copy.Name
		}
		require.Contains(t, expected, name)
		assert.Equal(t, expected[name], resource.Condition)
		if name == federationGrantName || name == storageGrantName {
			assert.NotEmpty(t, resource.SubscriptionID)
			assert.NotEmpty(t, resource.ResourceGroup)
		}
	}
}

func TestBicepToolVersionAndExplicitOverride(t *testing.T) {
	require.NoError(t, validateBicepVersion("Bicep CLI version "+bicep.Version.String()+" (hash)"))
	require.ErrorContains(t, validateBicepVersion("Bicep CLI version 0.38.33 (hash)"), "below azd SDK minimum")
	require.ErrorContains(t, validateBicepVersion("other CLI version 99.0.0"), "unrecognized Bicep")
	path, err := findBicep(t.Context(), filepath.Join(t.TempDir(), "missing-bicep"))
	require.Error(t, err)
	assert.Empty(t, path)
}
