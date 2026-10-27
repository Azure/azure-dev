// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

const contractClusterID = "/subscriptions/aks-sub/resourceGroups/aks-rg/providers/" +
	"Microsoft.ContainerService/managedClusters/customer"

func hostingContractInput(extra string) Input {
	return Input{
		RawAzureYAML: []byte("services:\n  project:\n    host: azure.ai.project\n    agentHosting:\n" +
			"      clusterResourceId: " + contractClusterID + "\n" + extra),
		ServiceName: "project", AcceptedHosts: []string{"azure.ai.project"},
	}
}

func hostingContractParameters(t *testing.T, result *Result) map[string]any {
	t.Helper()
	data, err := json.Marshal(result.Parameters["agentHosting"])
	require.NoError(t, err)
	var hosting map[string]any
	require.NoError(t, json.Unmarshal(data, &hosting))
	return hosting
}

func TestOnboardingContractCompactPublicInput(t *testing.T) {
	result, err := Synthesize(hostingContractInput(""))
	require.NoError(t, err)
	require.NotNil(t, result.Hosting)
	assert.Equal(t, AgentHostingInput{
		ClusterResourceID: contractClusterID,
	}, *result.Hosting)
	hosting := hostingContractParameters(t, result)
	assert.Equal(t, true, hosting["enabled"])
	assert.Equal(t, "ManagedCluster", hosting["hostingType"])
	assert.Equal(t, "default", hosting["name"])
	assert.Equal(t, contractClusterID, hosting["clusterResourceId"])
	for _, field := range []string{
		"hostingManagementIdentityResourceId", "workloadIdentityResourceId", "storageAccountResourceId",
	} {
		assert.Empty(t, hosting[field], "public synthesis must not invent deployment dependencies")
	}
	assert.Equal(t, true, result.Parameters["includeAcr"])
}

func TestOnboardingContractUsesStandardYAMLDecoding(t *testing.T) {
	for _, extra := range []string{
		"      storageAccountResourceId: null\n",
		"      storageAccountResourceId: \"\"\n",
	} {
		t.Run(extra, func(t *testing.T) {
			input := hostingContractInput(extra)
			result, err := Synthesize(input)
			require.NoError(t, err)
			assert.Empty(t, hostingContractParameters(t, result)["hostingManagementIdentityResourceId"])
		})
	}
	_, err := Synthesize(hostingContractInput(
		"      storageAccountResourceId: first\n      storageAccountResourceId: second\n"))
	require.ErrorContains(t, err, "already defined", "duplicate keys retain the YAML library's behavior")
}

func TestOnboardingContractSuppliedChoicesAndFixedHostingValues(t *testing.T) {
	plan := onboardingContractPlan()
	input := hostingContractInput("      storageAccountResourceId: " + plan.StorageAccountResourceID + "\n")
	result, err := Synthesize(input)
	require.NoError(t, err)
	require.NotNil(t, result.Hosting)
	assert.Equal(t, plan.StorageAccountResourceID, result.Hosting.StorageAccountResourceID)
	hosting := hostingContractParameters(t, result)
	assert.Equal(t, "ManagedCluster", hosting["hostingType"])
	assert.Equal(t, "default", hosting["name"])

	result, err = Synthesize(hostingContractInput("      storageAccountResourceId: \"   \"\n"))
	require.ErrorContains(t, err, "storageAccountResourceId")
	assert.Nil(t, result)
}

func TestOnboardingContractRejectsUnsupportedHostingProperties(t *testing.T) {
	for _, field := range []string{"hostingType", "name", "unknown"} {
		for _, value := range []string{
			"ManagedCluster", "default", "aks", "customer-hosting", `""`, "null", `"   "`,
			`"${REMOVED_HOSTING_INPUT}"`, "false", "123", "{}",
		} {
			for _, preserve := range []bool{false, true} {
				mode := map[bool]string{false: "provision", true: "eject"}[preserve]
				t.Run(field+"/"+value+"/"+mode, func(t *testing.T) {
					input := hostingContractInput("      " + field + ": " + value + "\n")
					input.PreserveVarRefs = preserve
					result, err := Synthesize(input)
					require.ErrorContains(t, err, "services.project.agentHosting."+field+": unsupported property")
					assert.Nil(t, result)
				})
			}
		}
	}
}

func TestOnboardingContractHostingFileReferences(t *testing.T) {
	for _, scope := range []string{"project", "agentHosting"} {
		for _, format := range []string{"yaml", "json"} {
			for _, field := range []string{"", "hostingType", "name", "unknown"} {
				t.Run(scope+"/"+format+"/"+field, func(t *testing.T) {
					hosting := map[string]any{"clusterResourceId": contractClusterID}
					if field != "" {
						hosting[field] = map[string]any{
							"hostingType": "ManagedCluster", "name": "default", "unknown": true,
						}[field]
					}
					var definition any = hosting
					raw := "services:\n  project:\n    host: azure.ai.project\n    agentHosting:\n" +
						"      $ref: ./input." + format + "\n"
					if scope == "project" {
						definition = map[string]any{"host": "azure.ai.project", "agentHosting": hosting}
						raw = "services:\n  project:\n    $ref: ./input." + format + "\n"
					}
					var data []byte
					var err error
					if format == "yaml" {
						data, err = yaml.Marshal(definition)
					} else {
						data, err = json.Marshal(definition)
					}
					require.NoError(t, err)
					dir := t.TempDir()
					require.NoError(t, os.WriteFile(filepath.Join(dir, "input."+format), data, 0o600))
					input := Input{
						RawAzureYAML: []byte(raw), ServiceName: "project", ProjectRoot: dir, PreserveVarRefs: true,
					}
					result, err := Synthesize(input)
					if field != "" {
						require.ErrorContains(t, err, "services.project.agentHosting."+field+": unsupported property")
						assert.Nil(t, result)
						return
					}
					require.NoError(t, err)
					resolved := hostingContractParameters(t, result)
					assert.Equal(t, "ManagedCluster", resolved["hostingType"])
					assert.Equal(t, "default", resolved["name"])
					assert.Equal(t, contractClusterID, resolved["clusterResourceId"])
				})
			}
		}
	}
}
