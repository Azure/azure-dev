// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagedNodeSubnetEvidence(t *testing.T) {
	for _, scenario := range []string{
		"valid", "case insensitive bindings", "paged", "multiple selected VMSS", "additional overlapping VMSS",
		"node overlap", "missing node RG", "invalid node RG", "wrong cluster", "wrong pool ID", "missing pool name",
		"missing managedBy", "wrong managedBy", "wrong RG ID", "group forbidden", "group missing",
		"missing pool tag", "only unrelated pool", "wrong VMSS RG", "missing VMSS state", "failed VMSS", "empty VMSS list",
		"missing VMSS properties", "missing VM profile", "missing network", "missing NICs", "nil NIC", "nil IP",
		"missing IP configurations", "missing subnet reference", "incomplete additional NIC",
		"invalid subnet ID", "VMSS forbidden", "partial list forbidden", "missing node CIDR", "wrong node ID",
		"node forbidden", "node missing", "missing node state", "explicit subnet unchanged",
	} {
		t.Run(scenario, func(t *testing.T) {
			f := newNetworkARMContract(t)
			input := contractPlanInput()
			input.StorageID, input.RegistryID = "", ""
			input.AgentSubnetID = resourceValue(f.vnet.ID) + "/subnets/supplied"
			plan, err := BuildPlan(input)
			require.NoError(t, err)
			f.fixture.Cluster.Properties.NodeResourceGroup = new("actual-nodes-rg")
			f.fixture.Pools[1].Properties.VnetSubnetID = nil
			groupID := "/subscriptions/aks-sub/resourceGroups/actual-nodes-rg"
			nodeID := groupID + "/providers/Microsoft.Network/virtualNetworks/actual-vnet/subnets/actual-nodes"
			group := armresources.ResourceGroup{ID: new(groupID), ManagedBy: new(plan.Cluster.ID)}
			node := armnetwork.Subnet{ID: new(nodeID), Properties: &armnetwork.SubnetPropertiesFormat{
				AddressPrefix: new("10.224.0.0/16"), ProvisioningState: new(armnetwork.ProvisioningStateSucceeded),
			}}
			f.vnet.Properties.AddressSpace.AddressPrefixes = []*string{new("10.225.0.0/16")}
			f.subnet = map[string]any{"id": plan.AgentSubnet.ID, "properties": map[string]any{
				"addressPrefix": "10.225.0.0/24", "provisioningState": "Succeeded",
				"delegations": []any{map[string]any{"properties": map[string]any{
					"serviceName": "Microsoft.App/environments",
				}}},
			}}
			var selected armcompute.VirtualMachineScaleSet
			resourceRuleContractJSON(t, &selected, `{
				"properties":{"provisioningState":"Succeeded","virtualMachineProfile":{"networkProfile":{
					"networkInterfaceConfigurations":[{"properties":{"ipConfigurations":[{"properties":{"subnet":{}}}]} }]
				}}},
				"tags":{"aks-managed-poolName":"agents"}
			}`)
			selected.ID = new(groupID + "/providers/Microsoft.Compute/virtualMachineScaleSets/unpredictable-name")
			ip := selected.Properties.VirtualMachineProfile.NetworkProfile.
				NetworkInterfaceConfigurations[0].Properties.IPConfigurations[0]
			ip.Properties.Subnet.ID = new(nodeID)
			sets := []*armcompute.VirtualMachineScaleSet{{
				ID:   new(groupID + "/providers/Microsoft.Compute/virtualMachineScaleSets/unrelated"),
				Tags: map[string]*string{"aks-managed-poolName": new("system")},
			}, &selected}
			switch scenario {
			case "case insensitive bindings":
				group.ManagedBy = new(strings.ToUpper(plan.Cluster.ID))
				group.ID = new(strings.ToUpper(groupID))
				selected.Tags["aks-managed-poolName"] = new("AGENTS")
				selected.ID = new(strings.ToUpper(resourceValue(selected.ID)))
			case "multiple selected VMSS", "additional overlapping VMSS":
				raw, err := json.Marshal(selected)
				require.NoError(t, err)
				var copy armcompute.VirtualMachineScaleSet
				require.NoError(t, json.Unmarshal(raw, &copy))
				copy.ID = new(groupID + "/providers/Microsoft.Compute/virtualMachineScaleSets/second")
				if scenario == "additional overlapping VMSS" {
					copy.Properties.VirtualMachineProfile.NetworkProfile.NetworkInterfaceConfigurations[0].
						Properties.IPConfigurations[0].Properties.Subnet.ID = new(nodeID + "-second")
				}
				sets = append(sets, &copy)
			case "missing node RG":
				f.fixture.Cluster.Properties.NodeResourceGroup = nil
			case "invalid node RG":
				f.fixture.Cluster.Properties.NodeResourceGroup = new("../other")
			case "wrong cluster":
				f.fixture.Cluster.ID = new(plan.Cluster.ID + "-other")
			case "wrong pool ID":
				f.fixture.Pools[1].ID = new(plan.Cluster.ID + "-other/agentPools/agents")
			case "missing pool name":
				f.fixture.Pools[1].Name = nil
			case "missing managedBy":
				group.ManagedBy = nil
			case "wrong managedBy":
				group.ManagedBy = new(plan.Cluster.ID + "-other")
			case "wrong RG ID":
				group.ID = new(groupID + "-other")
			case "missing pool tag":
				selected.Tags = nil
			case "only unrelated pool":
				selected.Tags["aks-managed-poolName"] = new("system")
			case "wrong VMSS RG":
				selected.ID = new(strings.ReplaceAll(resourceValue(selected.ID), "actual-nodes-rg", "different"))
			case "missing VMSS state":
				selected.Properties.ProvisioningState = nil
			case "failed VMSS":
				selected.Properties.ProvisioningState = new("Failed")
			case "empty VMSS list":
				sets = nil
			case "missing VMSS properties":
				selected.Properties = nil
			case "missing VM profile":
				selected.Properties.VirtualMachineProfile = nil
			case "missing network":
				selected.Properties.VirtualMachineProfile.NetworkProfile = nil
			case "missing NICs":
				selected.Properties.VirtualMachineProfile.NetworkProfile.NetworkInterfaceConfigurations = nil
			case "nil NIC":
				selected.Properties.VirtualMachineProfile.NetworkProfile.NetworkInterfaceConfigurations[0] = nil
			case "nil IP":
				selected.Properties.VirtualMachineProfile.NetworkProfile.
					NetworkInterfaceConfigurations[0].Properties.IPConfigurations[0] = nil
			case "incomplete additional NIC":
				network := selected.Properties.VirtualMachineProfile.NetworkProfile
				network.NetworkInterfaceConfigurations = append(network.NetworkInterfaceConfigurations,
					&armcompute.VirtualMachineScaleSetNetworkConfiguration{})
			case "missing IP configurations":
				selected.Properties.VirtualMachineProfile.NetworkProfile.
					NetworkInterfaceConfigurations[0].Properties.IPConfigurations = nil
			case "missing subnet reference":
				ip.Properties.Subnet = nil
			case "invalid subnet ID":
				ip.Properties.Subnet.ID = new("not-a-subnet")
			case "missing node CIDR":
				node.Properties.AddressPrefix = nil
			case "wrong node ID":
				node.ID = new(nodeID + "-other")
			case "missing node state":
				node.Properties.ProvisioningState = nil
			case "node overlap":
				node.Properties.AddressPrefix = new("10.225.0.0/16")
			case "explicit subnet unchanged":
				f.fixture.Pools[1].Properties.VnetSubnetID = new(nodeID)
				group.ManagedBy = nil
			}
			base := f.options()
			var calls []string
			options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, request.Method, "node-subnet discovery must remain read-only")
				calls = append(calls, request.URL.Path)
				var body any
				switch {
				case strings.EqualFold(request.URL.Path, groupID):
					if scenario == "group forbidden" || scenario == "group missing" {
						status := http.StatusForbidden
						if scenario == "group missing" {
							status = http.StatusNotFound
						}
						return contractARMResponse(request, status, `{"error":{"code":"GroupEvidenceUnavailable"}}`), nil
					}
					body = group
				case strings.EqualFold(request.URL.Path, groupID+"/providers/Microsoft.Compute/virtualMachineScaleSets"):
					if scenario == "VMSS forbidden" || scenario == "partial list forbidden" &&
						request.URL.Query().Get("$skiptoken") != "" {
						return contractARMResponse(request, 403, `{"error":{"code":"VmssEvidenceUnavailable"}}`), nil
					}
					values := sets
					page := map[string]any{}
					if scenario == "paged" || scenario == "partial list forbidden" {
						if request.URL.Query().Get("$skiptoken") == "" {
							values = sets[:1]
							if scenario == "partial list forbidden" {
								values = sets[1:]
							}
							page["nextLink"] = "https://management.azure.com" + request.URL.Path +
								"?api-version=2025-04-01&$skiptoken=next"
						} else {
							values = sets[1:]
						}
					}
					page["value"] = values
					body = page
				case strings.EqualFold(request.URL.Path, nodeID):
					if scenario == "node forbidden" || scenario == "node missing" {
						status := http.StatusForbidden
						if scenario == "node missing" {
							status = http.StatusNotFound
						}
						return contractARMResponse(request, status, `{"error":{"code":"NodeEvidenceUnavailable"}}`), nil
					}
					body = node
				case strings.EqualFold(request.URL.Path, nodeID+"-second"):
					body = armnetwork.Subnet{ID: new(nodeID + "-second"), Properties: &armnetwork.SubnetPropertiesFormat{
						AddressPrefix: new("10.225.0.0/16"), ProvisioningState: new(armnetwork.ProvisioningStateSucceeded),
					}}
				default:
					return base.Transport.Do(request)
				}
				data, err := json.Marshal(body)
				require.NoError(t, err)
				return contractARMResponse(request, 200, string(data)), nil
			})
			checker := &sdkResourceChecker{credential: contractCredential{}, options: onboardingARMOptions(options)}
			report := checker.networkChecks(t.Context(), plan, &f.fixture.Cluster, f.fixture.Pools, nil)
			switch scenario {
			case "valid", "case insensitive bindings", "paged", "multiple selected VMSS", "explicit subnet unchanged":
				require.NoError(t, report.Err())
				assert.Contains(t, calls, nodeID)
			default:
				require.Error(t, report.Err())
				last := report[len(report)-1]
				if scenario == "node overlap" || scenario == "additional overlapping VMSS" {
					assert.Equal(t, Fail, last.Status)
					assert.Contains(t, last.Observed, "overlaps")
				} else {
					assert.Equal(t, Unverified, last.Status)
					assert.Contains(t, last.Missing, "Node subnet ARM CIDR metadata")
				}
			}
			if scenario == "explicit subnet unchanged" {
				assert.NotContains(t, calls, groupID, "explicit pool subnet must not require VMSS discovery")
			}
			if strings.Contains(scenario, "forbidden") || scenario == "group missing" || scenario == "node missing" {
				serviceErr, ok := errors.AsType[*azcore.ResponseError](report.Err())
				require.True(t, ok, "original service error must be preserved")
				expected := http.StatusForbidden
				if strings.HasSuffix(scenario, "missing") {
					expected = http.StatusNotFound
				}
				assert.Equal(t, expected, serviceErr.StatusCode)
			}
		})
	}
}
