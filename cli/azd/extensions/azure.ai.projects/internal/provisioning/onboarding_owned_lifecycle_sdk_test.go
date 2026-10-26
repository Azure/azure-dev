// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"azure.ai.projects/internal/onboarding"
	"azure.ai.projects/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type trackingBYOLifecycle struct {
	byoLifecycleContract
	events *[]string
}

func (l trackingBYOLifecycle) Start(
	_ context.Context, _ onboarding.Plan, _ onboarding.FoundrySpec, deploymentID string,
) error {
	*l.events = append(*l.events, "intent")
	if !strings.Contains(deploymentID, "/providers/Microsoft.Resources/deployments/") {
		return fmt.Errorf("expected a subscription deployment")
	}
	return nil
}

func (l trackingBYOLifecycle) Observe(context.Context, onboarding.Plan) error {
	*l.events = append(*l.events, "readback")
	return nil
}

func TestBYOProviderActualSDKSingleDeploymentCreateReuseAndNetworkSelections(t *testing.T) {
	const vnet = "/subscriptions/network-sub/resourceGroups/network-rg/providers/Microsoft.Network/virtualNetworks/network"
	const storage = "/subscriptions/storage-sub/resourceGroups/storage-rg/providers/Microsoft.Storage/storageAccounts/customer"
	const registry = "/subscriptions/registry-sub/resourceGroups/registry-rg/providers/Microsoft.ContainerRegistry/registries/customer"
	for _, suppliedStorage := range []bool{false, true} {
		for _, suppliedRegistry := range []bool{false, true} {
			for _, choice := range []string{
				"none", "managed", "customer subnet", "created customer subnet", "environment subnet",
			} {
				t.Run(fmt.Sprintf("storage=%t/registry=%t/network=%s", suppliedStorage, suppliedRegistry, choice), func(t *testing.T) {
					p := byoContractProvider(t)
					raw := "services:\n  project:\n    host: azure.ai.project\n    agentHosting:\n" +
						"      clusterResourceId: " + byoProviderCluster + "\n"
					if suppliedStorage {
						raw += "      storageAccountResourceId: " + storage + "\n"
					}
					if suppliedRegistry {
						p.virtualEnv = map[string]string{"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": registry}
					}
					if choice != "none" {
						raw += "    network:\n      peSubnet: {vnet: " + vnet + ", name: pe}\n"
					}
					subnetID := ""
					if choice == "customer subnet" || choice == "created customer subnet" || choice == "environment subnet" {
						subnetID = vnet + "/subnets/legacy-agents"
						prefix := ""
						if choice == "created customer subnet" {
							prefix = ", prefix: 10.0.1.0/24"
						}
						raw += "      agentSubnet: {vnet: " + vnet + ", name: legacy-agents" + prefix + "}\n"
					}
					if choice == "environment subnet" {
						raw = strings.ReplaceAll(raw, "name: legacy-agents", "name: '${FOUNDRY_AGENT_SUBNET_NAME}'")
						raw = strings.ReplaceAll(raw, "name: pe}", "name: '${FOUNDRY_PE_SUBNET_NAME}'}")
					}
					result, err := synthesis.Synthesize(synthesis.Input{
						RawAzureYAML: []byte(raw), ServiceName: "project", AcceptedHosts: []string{"azure.ai.project"},
						Env: map[string]string{
							"FOUNDRY_AGENT_SUBNET_NAME": "legacy-agents", "FOUNDRY_PE_SUBNET_NAME": "pe",
						},
					})
					require.NoError(t, err)
					p.synthResult = result
					var events []string
					p.byoPipeline = byoContractPipeline(&events, onboarding.Pass)
					p.byoLifecycle = trackingBYOLifecycle{events: &events}
					writes := 0
					p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
						Transport: byoHTTPContract(func(request *http.Request) (*http.Response, error) {
							require.Equal(t, http.MethodPut, request.Method, "no SDK dependency or role writes")
							require.Contains(t, request.URL.Path,
								"/subscriptions/foundry-sub/providers/Microsoft.Resources/deployments/")
							writes++
							events = append(events, "ARM")
							var body map[string]any
							require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
							properties := lifecycleObject(t, body["properties"])
							parameters := lifecycleObject(t, properties["parameters"])
							if choice == "environment subnet" {
								assert.Equal(t, "legacy-agents", lifecycleObject(t, parameters["agentSubnetName"])["value"])
								assert.Equal(t, "pe", lifecycleObject(t, parameters["peSubnetName"])["value"])
							}
							assert.NotContains(t, parameters, "byoRegistryAccessReady")
							require.Contains(t, parameters, "byoOnboarding")
							assert.Equal(t, choice != "none", lifecycleObject(t, parameters["enableNetworkIsolation"])["value"])
							if choice != "none" {
								assert.Equal(t, vnet, lifecycleObject(t, parameters["vnetId"])["value"])
								assert.Equal(t, choice == "managed", lifecycleObject(t, parameters["useManagedEgress"])["value"])
							}
							hosting := lifecycleObject(t, lifecycleObject(t, parameters["agentHosting"])["value"])
							assert.NotContains(t, hosting, "agentSubnetResourceId")
							output := `{"id":"` + request.URL.Path + `","properties":{"provisioningState":"Succeeded",` +
								`"outputs":{"AZURE_AI_PROJECT_NAME":{"type":"String","value":"created-project"},` +
								`"AZD_BYO_AKS_OWNERSHIP":{"type":"Object","value":{"owner":"` + p.byoPlan.Owner +
								`","roleAssignments":[]}}}}}`
							return contractLifecycleResponse(request, 200, output), nil
						}),
					}}
					for attempt := range 2 {
						deployed, err := p.Deploy(t.Context(), func(string) {})
						if choice == "none" || choice == "managed" {
							require.ErrorContains(t, err, "requires network.agentSubnet and network.peSubnet")
							assert.Zero(t, writes)
							assert.Empty(t, events, "invalid network fails before preflight, intent or ARM")
							break
						}
						require.NoError(t, err)
						assert.Equal(t, "created-project", deployed.Deployment.Outputs["AZURE_AI_PROJECT_NAME"].Value)
						ownership := deployed.Deployment.Outputs["AZD_BYO_AKS_OWNERSHIP"]
						require.NotNil(t, ownership)
						assert.Equal(t, "Object", ownership.Type)
						var outputOwnership map[string]any
						require.NoError(t, json.Unmarshal([]byte(ownership.Value), &outputOwnership))
						assert.Equal(t, p.byoPlan.Owner, outputOwnership["owner"])
						assert.Equal(t, attempt+1, writes, "one top-level ARM submission per provision")
						require.NotNil(t, p.byoPlan)
						assert.Equal(t, subnetID, p.byoPlan.AgentSubnet.ID)
						assert.Equal(t, choice == "customer subnet" || choice == "environment subnet", p.byoPlan.AgentSubnet.Supplied)
						if choice == "environment subnet" {
							for _, name := range []string{"legacy-agents", "pe"} {
								assert.Contains(t, p.byoPlan.Operations, onboarding.Operation{
									CheckID: "P03", Action: "Microsoft.Network/virtualNetworks/subnets/join/action",
									Scope: vnet + "/subnets/" + name,
								})
							}
						}
						assert.Equal(t, suppliedStorage, p.byoPlan.Storage.Supplied)
						assert.Equal(t, suppliedRegistry, p.byoPlan.Registry.Supplied)
					}
					if choice != "none" && choice != "managed" {
						assert.Equal(t, []string{
							"resources", "authorization", "intent", "ARM", "readback",
							"resources", "authorization", "intent", "ARM", "readback",
						}, events)
					}
				})
			}
		}
	}
}

func TestBYORejectsBicepThatDeclaresButIgnoresPrivateOwnershipInputs(t *testing.T) {
	compiler, err := exec.LookPath("bicep")
	if err != nil {
		t.Skip("Bicep CLI is required for native ownership-binding regression")
	}
	templates, err := fs.Sub(synthesis.TemplatesFS(), "templates")
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, os.CopyFS(dir, templates))
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })
	module := filepath.Join("modules", "resources.bicep")
	raw, err := root.ReadFile(module)
	require.NoError(t, err)
	const binding = "var foundryAccountName = empty(byoAccountName) ? " +
		"'${abbrs.cognitiveServicesAccounts}${resourceToken}' : byoAccountName"
	require.Contains(t, string(raw), binding)
	changed := strings.Replace(string(raw), binding, "var foundryAccountName = 'untracked-account'", 1)
	require.NoError(t, root.WriteFile(module, []byte(changed), 0o600))
	// #nosec G204 -- Trusted compiler; all arguments address a test-owned temporary fixture.
	compiled, err := exec.CommandContext(t.Context(), compiler, "build", filepath.Join(dir, "main.bicep"), "--stdout").Output()
	require.NoError(t, err)
	var template map[string]any
	require.NoError(t, json.Unmarshal(compiled, &template))
	parameters := lifecycleObject(t, template["parameters"])
	require.Contains(t, parameters, "byoOwner")
	require.Contains(t, parameters, "byoAccountName")
	p := byoContractProvider(t)
	p.onDiskSource = &templateSource{mode: templateModeBicep, armTemplate: template, sourcePath: dir}
	p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
		Transport: byoHTTPContract(func(*http.Request) (*http.Response, error) {
			t.Fatal("altered ownership binding must be rejected before Azure access or deployment")
			return nil, nil
		}),
	}}
	_, err = p.Deploy(t.Context(), func(string) {})
	require.ErrorContains(t, err, "standard ownership-bound template")
}
