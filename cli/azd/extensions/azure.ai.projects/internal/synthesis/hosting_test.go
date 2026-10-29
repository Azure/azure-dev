// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"encoding/json"
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

func hostingYAML() string {
	return `services:
  project:
    host: azure.ai.project
    agentHosting:
      clusterResourceId: ` + hostingClusterID + `
    network:
      agentSubnet: {vnet: ` + hostingVNetID + `, name: agents}
      peSubnet: {vnet: ` + hostingVNetID + `, name: endpoints}
`
}

func TestHostingDefaultSynthesis(t *testing.T) {
	for _, preserve := range []bool{false, true} {
		result, err := Synthesize(Input{
			RawAzureYAML: []byte(hostingYAML()), ServiceName: "project", PreserveVarRefs: preserve,
		})
		require.NoError(t, err)
		require.NotNil(t, result.Hosting)
		assert.Equal(t, hostingClusterID, result.Hosting.ClusterResourceID)
		assert.Equal(t, true, result.Parameters["includeAcr"], "project-only hosting still needs the native registry")
		assert.Equal(t, NetworkModeByo, result.NetworkMode)
		hosting, ok := result.Parameters["agentHosting"].(agentHostingParameter)
		require.True(t, ok)
		assert.Equal(t, "ManagedCluster", hosting.HostingType)
		assert.Equal(t, "default", hosting.Name)
		assert.Equal(t, hostingClusterID, hosting.ClusterResourceID)
		assert.NotContains(t, result.Parameters, "containerRegistryResourceId")
	}
}

func TestHostingRejectsUnsupportedSelections(t *testing.T) {
	for _, test := range []struct{ field, value string }{
		{"storageAccountResourceId", `""`},
		{"hostingManagementIdentityResourceId", "null"},
		{"workloadIdentityResourceId", `""`},
		{"registryResourceId", "/subscriptions/s/resourceGroups/r/providers/Microsoft.ContainerRegistry/registries/r"},
		{"unknown", "null"},
	} {
		t.Run(test.field, func(t *testing.T) {
			raw := strings.Replace(hostingYAML(), "    network:",
				"      "+test.field+": "+test.value+"\n    network:", 1)
			result, err := Synthesize(Input{RawAzureYAML: []byte(raw), ServiceName: "project"})
			require.ErrorContains(t, err, "unsupported property")
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
		{"empty cluster", strings.Replace(hostingYAML(), hostingClusterID, `""`, 1), "required"},
		{"wrong type", strings.Replace(hostingYAML(), hostingClusterID, hostingVNetID, 1), "managedClusters resource ID"},
		{"credential URL", strings.Replace(hostingYAML(), hostingClusterID,
			"https://username:secret@host.test/cluster?sig=private#fragment", 1), "managedClusters resource ID"},
		{"credential VNet URL", strings.ReplaceAll(hostingYAML(), hostingVNetID,
			`"https://username:secret@host.test/vnet?sig=private#fragment"`), "virtualNetworks id"},
		{"missing network", strings.Split(hostingYAML(), "    network:")[0], "requires network.agentSubnet"},
		{"managed egress", strings.Replace(hostingYAML(),
			"      agentSubnet: {vnet: "+hostingVNetID+", name: agents}\n", "", 1), "requires network.agentSubnet"},
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
	config = strings.Replace(config, "    network:", "      storageAccountResourceId: ''\n    network:", 1)
	require.NoError(t, os.WriteFile(path, []byte(config), 0o600))
	_, err = Synthesize(input)
	require.ErrorContains(t, err, "storageAccountResourceId: unsupported property")
}

func TestHostingSubnetNamesResolveBeforeComparison(t *testing.T) {
	raw := strings.ReplaceAll(hostingYAML(), "name: agents", `name: "${AGENT_SUBNET}"`)
	raw = strings.ReplaceAll(raw, "name: endpoints", `name: "${PE_SUBNET}"`)
	input := Input{RawAzureYAML: []byte(raw), ServiceName: "project",
		Env: map[string]string{"AGENT_SUBNET": "agents", "PE_SUBNET": "endpoints"}}
	result, err := Synthesize(input)
	require.NoError(t, err)
	assert.Equal(t, "agents", result.Parameters["agentSubnetName"])
	input.Env["PE_SUBNET"] = "agents"
	_, err = Synthesize(input)
	require.ErrorContains(t, err, "must differ")
}

func TestHostingSchemaMatchesRuntimeSelections(t *testing.T) {
	raw, err := os.ReadFile(schemaPath)
	require.NoError(t, err)
	var schemaDocument any
	require.NoError(t, json.Unmarshal(raw, &schemaDocument))
	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource("hosting.json", schemaDocument))
	schema, err := compiler.Compile("hosting.json")
	require.NoError(t, err)
	hosting := map[string]any{"clusterResourceId": hostingClusterID}
	service := map[string]any{"agentHosting": hosting, "network": map[string]any{
		"agentSubnet": map[string]any{"vnet": hostingVNetID, "name": "agents"},
		"peSubnet":    map[string]any{"vnet": hostingVNetID, "name": "endpoints"},
	}}
	require.NoError(t, schema.Validate(service))
	hosting["storageAccountResourceId"] = ""
	require.Error(t, schema.Validate(service))
	delete(hosting, "storageAccountResourceId")
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

func TestHostingGraphUsesOneNativeRegistry(t *testing.T) {
	raw, err := ARMTemplate()
	require.NoError(t, err)
	var template any
	require.NoError(t, json.Unmarshal(raw, &template))
	var registries, identities, storages, connections, roleTemplates int
	var visit func(any)
	visit = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			switch value["type"] {
			case "Microsoft.ContainerRegistry/registries":
				registries++
				assert.Equal(t, "[parameters('name')]", value["name"])
				sku, ok := value["sku"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, "Premium", sku["name"])
			case "Microsoft.ManagedIdentity/userAssignedIdentities":
				identities++
			case "Microsoft.Storage/storageAccounts":
				storages++
				properties, ok := value["properties"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, false, properties["allowSharedKeyAccess"])
				assert.Equal(t, false, properties["allowBlobPublicAccess"])
				assert.Equal(t, true, properties["supportsHttpsTrafficOnly"])
			case "Microsoft.CognitiveServices/accounts/projects/connections":
				connections++
				properties, ok := value["properties"].(map[string]any)
				require.True(t, ok)
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
	text := string(raw)
	for _, required := range []string{
		"agentHostingConfigurations", "ManagedCluster", "hostingManagementIdentityResourceId", "workloadIdentityResourceId",
		"LegacyRegistryPermissions", "kubeletObjectId", "ContainerRegistry",
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
		Resources  map[string]struct {
			Properties struct {
				Template struct {
					Variables map[string]json.RawMessage
					Resources map[string]struct {
						Condition  string
						DependsOn  []string
						Properties json.RawMessage
					}
				}
			}
		}
	}
	require.NoError(t, json.Unmarshal(raw, &graph))
	require.Contains(t, graph.Resources, "resources")
	foundry := graph.Resources["resources"].Properties.Template
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
	account := foundry.Resources["foundryAccount"]
	assert.Contains(t, account.DependsOn, "prerequisites")
	assert.Contains(t, account.DependsOn, "network")
	var properties string
	require.NoError(t, json.Unmarshal(account.Properties, &properties))
	for _, binding := range []string{
		"resourceId('Microsoft.ManagedIdentity/userAssignedIdentities', variables('hostingIdentityName'))",
		"resourceId('Microsoft.ManagedIdentity/userAssignedIdentities', variables('workloadIdentityName'))",
		"resourceId('Microsoft.Storage/storageAccounts', variables('storageAccountName'))",
	} {
		assert.Contains(t, properties, binding, "account preflight must receive concrete IDs, not module output references")
	}
	assert.Contains(t, properties, "if(parameters('agentHosting').enabled, createObject('agentHostingConfigurations'")
	assert.True(t, strings.HasSuffix(properties, "createObject())))]"),
		"native accounts must omit disabled agent hosting rather than send an empty configuration")
	for _, output := range []string{
		"AZURE_AI_PROJECT_ID", "FOUNDRY_PROJECT_ENDPOINT", "AZURE_CONTAINER_REGISTRY_RESOURCE_ID",
		"AZURE_CONTAINER_REGISTRY_ENDPOINT", "AZURE_AI_PROJECT_ACR_CONNECTION_NAME",
	} {
		assert.Contains(t, graph.Outputs, output)
	}
	assert.Contains(t, graph.Parameters, "kubeletObjectId")
	assert.Contains(t, graph.Parameters, "federatedIdentityRoleDefinitionId")
}

func TestBicepToolVersionAndExplicitOverride(t *testing.T) {
	require.NoError(t, validateBicepVersion("Bicep CLI version "+bicep.Version.String()+" (hash)"))
	require.ErrorContains(t, validateBicepVersion("Bicep CLI version 0.38.33 (hash)"), "below azd SDK minimum")
	require.ErrorContains(t, validateBicepVersion("other CLI version 99.0.0"), "unrecognized Bicep")
	path, err := findBicep(t.Context(), filepath.Join(t.TempDir(), "missing-bicep"))
	require.Error(t, err)
	assert.Empty(t, path)
}
