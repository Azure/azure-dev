// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSubscription = "/subscriptions/target-sub"
const testGroup = testSubscription + "/resourceGroups/target-rg"
const testCluster = "/subscriptions/cluster-sub/resourceGroups/cluster-rg/providers/" +
	"Microsoft.ContainerService/managedClusters/cluster"
const testStorage = testGroup + "/providers/Microsoft.Storage/storageAccounts/customer"
const testHosting = testGroup + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/hosting"
const testWorkload = testGroup + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/workload"
const testKubeletObjectID = "11111111-1111-4111-8111-111111111111"

type testCredential struct {
	err error
}

func (c testCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "offline-token", ExpiresOn: time.Now().Add(time.Hour)}, c.err
}

type readReply struct {
	status int
	body   any
}

type readRequest struct {
	method, path string
}

type readFake struct {
	mu       sync.Mutex
	replies  map[string]readReply
	requests []readRequest
	handler  func(*http.Request) (readReply, bool)
}

func (f *readFake) Do(request *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.requests = append(f.requests, readRequest{
		request.Method, request.URL.Path,
	})
	f.mu.Unlock()
	if request.Method != http.MethodGet {
		return nil, fmt.Errorf("preflight attempted a mutation: %s %s", request.Method, request.URL.Path)
	}
	reply, ok := f.replies[strings.ToLower(request.URL.Path)]
	if f.handler != nil {
		if handled, found := f.handler(request); found {
			reply, ok = handled, true
		}
	}
	if !ok {
		reply = readReply{404, map[string]any{"error": map[string]string{
			"code": "ResourceNotFound", "message": "offline resource does not exist",
		}}}
	}
	raw, err := json.Marshal(reply.body)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: reply.status, Request: request, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(string(raw))),
	}, nil
}

func (f *readFake) options() *arm.ClientOptions {
	return &arm.ClientOptions{ClientOptions: policy.ClientOptions{
		Transport: f, Retry: policy.RetryOptions{MaxRetries: -1},
	}}
}

func (f *readFake) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, request := range f.requests {
		if strings.EqualFold(request.path, path) {
			count++
		}
	}
	return count
}

func suppliedPlan(t *testing.T) Plan {
	t.Helper()
	return Plan{
		SubscriptionID: "target-sub",
		Cluster: Resource{
			ID: testCluster, Parent: "/subscriptions/cluster-sub/resourceGroups/cluster-rg", Supplied: true,
		},
		Storage:  Resource{ID: testStorage, Parent: testGroup, Supplied: true},
		Hosting:  Resource{ID: testHosting, Parent: testGroup, Supplied: true},
		Workload: Resource{ID: testWorkload, Parent: testGroup, Supplied: true},
	}
}

func storageBody() map[string]any {
	return map[string]any{
		"id": testStorage, "kind": "StorageV2", "sku": map[string]any{"name": "Standard_ZRS"},
		"properties": map[string]any{
			"provisioningState": "Succeeded", "accessTier": "Hot", "isHnsEnabled": false,
			"supportsHttpsTrafficOnly": true, "minimumTlsVersion": "TLS1_2",
		},
	}
}

func resourceFake() *readFake {
	fake := &readFake{replies: map[string]readReply{}}
	for _, subscription := range []string{"target-sub", "cluster-sub"} {
		fake.replies["/subscriptions/"+subscription] = readReply{200, map[string]any{"tenantId": "tenant"}}
	}
	fake.replies[strings.ToLower(testStorage)] = readReply{200, storageBody()}
	for _, item := range []struct{ id, principal string }{
		{testHosting, "hosting-principal"}, {testWorkload, "workload-principal"},
	} {
		fake.replies[strings.ToLower(item.id)] = readReply{200, map[string]any{
			"id": item.id, "properties": map[string]any{
				"principalId": item.principal, "clientId": item.principal + "-client", "tenantId": "tenant",
			},
		}}
	}
	fake.replies[strings.ToLower(testCluster)] = readReply{200, map[string]any{
		"id": testCluster, "location": "westus",
		"sku":      map[string]any{"name": "Base"},
		"identity": map[string]any{"type": "SystemAssigned"},
		"properties": map[string]any{
			"currentKubernetesVersion": "1.31.0",
			"networkProfile": map[string]any{
				"networkPlugin": "azure", "networkPluginMode": "overlay", "networkDataplane": "cilium",
			},
			"identityProfile":   map[string]any{"kubeletidentity": map[string]any{"objectId": testKubeletObjectID}},
			"oidcIssuerProfile": map[string]any{"enabled": true, "issuerURL": "https://issuer.example.test/"},
			"securityProfile":   map[string]any{"workloadIdentity": map[string]any{"enabled": true}},
		},
	}}
	fake.replies[strings.ToLower(testCluster+"/agentPools")] = readReply{200, map[string]any{"value": []any{
		map[string]any{"id": testCluster + "/agentPools/system", "name": "system", "properties": map[string]any{
			"mode": "System", "vmSize": "Standard_E4s_v5", "count": 1,
		}},
		map[string]any{"id": testCluster + "/agentPools/agents", "name": "agents", "properties": map[string]any{
			"mode": "User", "osSKU": "AzureLinux", "vmSize": "Standard_D8as_v5", "count": 1,
			"workloadRuntime": "KataVmIsolation", "nodeImageVersion": "AKSAzureLinux-V3katagen2-202608.14.0",
			"nodeLabels": map[string]any{"agents.ai.azure.com/sandbox-node": "true"},
		}},
	}}}
	var skus []any
	for _, size := range []struct {
		name, cpu string
	}{{"Standard_E4s_v5", "4"}, {"Standard_D8as_v5", "8"}} {
		skus = append(skus, map[string]any{
			"name": size.name, "resourceType": "virtualMachines", "locations": []string{"westus"},
			"capabilities": []any{map[string]any{"name": "vCPUs", "value": size.cpu},
				map[string]any{"name": "MemoryGB", "value": "32"}},
		})
	}
	fake.replies["/subscriptions/cluster-sub/providers/microsoft.compute/skus"] =
		readReply{200, map[string]any{"value": skus}}
	return fake
}

func checkWithID(t *testing.T, report Report, id string) Check {
	t.Helper()
	for _, check := range report {
		if check.ID == id {
			return check
		}
	}
	t.Fatalf("missing check %s in %#v", id, report)
	return Check{}
}

func TestResourceCheckerReadsOnlySupportingResources(t *testing.T) {
	fake := resourceFake()
	facts, report, err := NewResourceChecker(testCredential{}, fake.options(), nil).Check(t.Context(), suppliedPlan(t))
	require.NoError(t, err)
	assert.Equal(t, "hosting-principal", facts.HostingIdentity.PrincipalID)
	assert.Equal(t, "workload-principal", facts.WorkloadIdentity.PrincipalID)
	for _, id := range []string{"storage-type", "storage-security"} {
		assert.Equal(t, Pass, checkWithID(t, report, id).Status)
	}
	for _, id := range aksCheckIDs {
		assert.Equal(t, Pass, checkWithID(t, report, id).Status)
	}
	assert.Len(t, fake.requests, 8, "two subscription tenants, three supporting resources, AKS, pools and SKUs")
	assert.Equal(t, 1, fake.count(testCluster))
	assert.Equal(t, 1, fake.count(testCluster+"/agentPools"))
	for _, request := range fake.requests {
		assert.Equal(t, http.MethodGet, request.method)
		assert.NotContains(t, request.path, "Microsoft.Network")
	}
}

func TestResourceCheckerSelectsOnePoolWithCompleteNetworkEvidence(t *testing.T) {
	for _, test := range []struct {
		name, firstNetwork, secondNetwork, secondConfig, want string
		noSubnet, firstInvalid, blocked                       bool
		invalidAgentSubnet, invalidCluster                    bool
	}{
		{name: "both pass", want: "baseline"},
		{name: "first pool already qualifies", secondNetwork: "forbidden", want: "first"},
		{name: "overlapping first subnet", firstNetwork: "overlap", want: "second"},
		{name: "unreadable first subnet", firstNetwork: "forbidden", want: "second"},
		{name: "missing first subnet CIDR", firstNetwork: "missing CIDR", want: "second"},
		{name: "mismatched first subnet ID", firstNetwork: "wrong ID", want: "second"},
		{name: "unfinished first subnet", firstNetwork: "updating", want: "second"},
		{name: "unresolved managed first subnet", firstNetwork: "managed", want: "second"},
		{name: "neither subnet qualifies", firstNetwork: "overlap", secondNetwork: "forbidden",
			want: "baseline", blocked: true},
		{name: "neither subnet readable", firstNetwork: "forbidden", secondNetwork: "forbidden",
			want: "baseline", blocked: true},
		{name: "cannot combine runtime and network", firstNetwork: "overlap", secondConfig: "runtime",
			want: "first", blocked: true},
		{name: "cannot combine image and network", firstNetwork: "overlap", secondConfig: "image",
			want: "first", blocked: true},
		{name: "cannot combine label and network", firstNetwork: "overlap", secondConfig: "label",
			want: "first", blocked: true},
		{name: "cannot combine count and network", firstNetwork: "overlap", secondConfig: "count",
			want: "first", blocked: true},
		{name: "no fully configured pool", firstInvalid: true, secondConfig: "image",
			want: "second", blocked: true},
		{name: "no existing Agent subnet", firstNetwork: "forbidden", noSubnet: true, want: "baseline"},
		{name: "invalid Agent subnet still blocks", invalidAgentSubnet: true, want: "baseline", blocked: true},
		{name: "cluster failure still blocks with a qualifying pool", firstNetwork: "overlap", invalidCluster: true,
			want: "second", blocked: true},
	} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reverse=%t", test.name, reverse), func(t *testing.T) {
				fake := resourceFake()
				raw, err := json.Marshal(fake.replies[strings.ToLower(testCluster+"/agentPools")].body)
				require.NoError(t, err)
				var response struct {
					Value []*armcontainerservice.AgentPool `json:"value"`
				}
				require.NoError(t, json.Unmarshal(raw, &response))
				require.Len(t, response.Value, 2)
				first := response.Value[1]
				require.NotNil(t, first)
				require.NotNil(t, first.Properties)
				second := &armcontainerservice.AgentPool{
					ID: new(testCluster + "/agentPools/alternative"), Name: new("alternative"),
					Properties: new(*first.Properties),
				}
				second.Properties.NodeLabels = maps.Clone(first.Properties.NodeLabels)
				first.Properties.VMSize, second.Properties.VMSize = new("First_Size"), new("Second_Size")
				networkID := testGroup + "/providers/Microsoft.Network/virtualNetworks/network"
				agentSubnetID := networkID + "/subnets/agents"
				firstSubnetID, secondSubnetID := networkID+"/subnets/first", networkID+"/subnets/second"
				first.Properties.VnetSubnetID, second.Properties.VnetSubnetID = new(firstSubnetID), new(secondSubnetID)
				if test.firstInvalid {
					first.Properties.Count = new(int32(0))
				}
				switch test.secondConfig {
				case "runtime":
					second.Properties.WorkloadRuntime = new(armcontainerservice.WorkloadRuntime("OCIContainer"))
				case "image":
					second.Properties.NodeImageVersion = nil
				case "label":
					second.Properties.NodeLabels = nil
				case "count":
					second.Properties.Count = new(int32(0))
				}
				for i, state := range []string{test.firstNetwork, test.secondNetwork} {
					subnetID := []string{firstSubnetID, secondSubnetID}[i]
					body := &armnetwork.Subnet{ID: new(subnetID), Properties: &armnetwork.SubnetPropertiesFormat{
						AddressPrefix: new("10.0.2.0/24"), ProvisioningState: new(armnetwork.ProvisioningStateSucceeded),
					}}
					switch state {
					case "overlap":
						body.Properties.AddressPrefix = new("10.0.1.0/24")
					case "missing CIDR":
						body.Properties.AddressPrefix = nil
					case "wrong ID":
						body.ID = new(subnetID + "-other")
					case "updating":
						body.Properties.ProvisioningState = new(armnetwork.ProvisioningStateUpdating)
					case "managed":
						first.Properties.VnetSubnetID = nil
					}
					reply := readReply{200, body}
					if state == "forbidden" {
						reply = readReply{403, map[string]any{"error": map[string]string{
							"code": "AuthorizationFailed", "message": "offline subnet forbidden",
						}}}
					}
					fake.replies[strings.ToLower(subnetID)] = reply
				}
				response.Value = append(response.Value, second)
				if reverse {
					response.Value[1], response.Value[2] = second, first
				}
				fake.replies[strings.ToLower(testCluster+"/agentPools")] = readReply{200, response}
				fake.replies[strings.ToLower(networkID)] = readReply{200, &armnetwork.VirtualNetwork{
					ID: new(networkID), Properties: &armnetwork.VirtualNetworkPropertiesFormat{
						ProvisioningState: new(armnetwork.ProvisioningStateSucceeded),
						AddressSpace:      &armnetwork.AddressSpace{AddressPrefixes: []*string{new("10.0.0.0/16")}},
					},
				}}
				fake.replies[strings.ToLower(agentSubnetID)] = readReply{200, &armnetwork.Subnet{
					ID: new(agentSubnetID), Properties: &armnetwork.SubnetPropertiesFormat{
						AddressPrefix: new("10.0.1.0/27"),
						Delegations: []*armnetwork.Delegation{{Properties: &armnetwork.ServiceDelegationPropertiesFormat{
							ServiceName: new("Microsoft.App/environments"),
						}}},
					},
				}}
				plan := suppliedPlan(t)
				if !test.noSubnet {
					plan.AgentSubnet = Resource{ID: agentSubnetID, Supplied: true}
				}
				if test.invalidAgentSubnet {
					fake.replies[strings.ToLower(agentSubnetID)] = readReply{200, &armnetwork.Subnet{
						ID: new(agentSubnetID), Properties: &armnetwork.SubnetPropertiesFormat{
							AddressPrefix: new("10.0.1.0/27"),
						},
					}}
				}
				if test.invalidCluster {
					body, ok := fake.replies[strings.ToLower(testCluster)].body.(map[string]any)
					require.True(t, ok)
					body["sku"] = map[string]any{"name": "Automatic"}
				}
				_, report, err := NewResourceChecker(testCredential{}, fake.options(), nil).Check(t.Context(), plan)
				if test.blocked {
					require.Error(t, err)
					require.Error(t, report.Err())
					if test.firstNetwork == "forbidden" {
						service, ok := errors.AsType[*azcore.ResponseError](err)
						require.True(t, ok)
						assert.Equal(t, http.StatusForbidden, service.StatusCode)
					}
				} else {
					require.NoError(t, err)
				}
				selected, otherSubnetID := first, secondSubnetID
				if test.want == "second" || test.want == "baseline" && reverse {
					selected, otherSubnetID = second, firstSubnetID
				}
				for _, id := range []string{"agent-pool", "agent-image", "agent-label"} {
					check := checkWithID(t, report, id)
					assert.Equal(t, *selected.ID, check.Scope)
					if !test.blocked {
						assert.Equal(t, Pass, check.Status)
					}
				}
				capacity := checkWithID(t, report, "agent-pool")
				assert.Contains(t, capacity.Warning, "Pool "+*selected.Name+":")
				assert.Equal(t, Pass, checkWithID(t, report, "system-pool").Status)
				for _, check := range report {
					assert.NotEqual(t, otherSubnetID, check.Scope, "do not retain another pool's network report")
				}
				assert.LessOrEqual(t, fake.count(firstSubnetID), 1, "reuse the diagnostic candidate's network evidence")
				assert.LessOrEqual(t, fake.count(secondSubnetID), 1, "reuse the diagnostic candidate's network evidence")
				if test.secondConfig != "" && test.want == "first" {
					assert.Zero(t, fake.count(secondSubnetID), "an incompatible pool cannot qualify through its network")
				}
				if !test.blocked && !test.noSubnet {
					for _, check := range report {
						assert.False(t, check.blocks(), "all required checks must pass for the same pool")
					}
				}
				if test.noSubnet {
					assert.Equal(t, NotApplicable, checkWithID(t, report, "agent-subnet").Status)
					assert.Zero(t, fake.count(networkID))
					assert.Zero(t, fake.count(firstSubnetID))
					assert.Zero(t, fake.count(secondSubnetID))
				}
				if test.name == "both pass" {
					assert.Zero(t, fake.count(otherSubnetID), "do not read alternatives after a complete match")
				}
				for _, request := range fake.requests {
					assert.Equal(t, http.MethodGet, request.method)
				}
			})
		}
	}
}

func TestResourceCheckerRequiresStandardAKS(t *testing.T) {
	for _, test := range []struct {
		name   string
		sku    any
		status Status
	}{
		{"Base without tier", map[string]any{"name": "Base"}, Pass},
		{"Base Free tier", map[string]any{"name": "Base", "tier": "Free"}, Pass},
		{"Base Standard tier", map[string]any{"name": "Base", "tier": "Standard"}, Pass},
		{"Base Premium tier", map[string]any{"name": "Base", "tier": "Premium"}, Pass},
		{"case insensitive Base", map[string]any{"name": "base"}, Pass},
		{"Automatic Standard tier", map[string]any{"name": "Automatic", "tier": "Standard"}, Fail},
		{"unknown name", map[string]any{"name": "Unknown"}, Fail},
		{"missing SKU", nil, Unverified},
		{"null SKU", nil, Unverified},
		{"missing name", map[string]any{"tier": "Standard"}, Unverified},
		{"null name", map[string]any{"name": nil}, Unverified},
		{"empty name", map[string]any{"name": ""}, Unverified},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := resourceFake()
			body, ok := fake.replies[strings.ToLower(testCluster)].body.(map[string]any)
			require.True(t, ok)
			body["sku"] = test.sku
			if test.name == "missing SKU" {
				delete(body, "sku")
			}
			_, report, err := NewResourceChecker(testCredential{}, fake.options(), nil).Check(t.Context(), suppliedPlan(t))
			check := checkWithID(t, report, "aks-type")
			assert.Equal(t, test.status, check.Status)
			assert.Equal(t, testCluster, check.Scope)
			assert.True(t, check.Mandatory)
			assert.Equal(t, 1, fake.count(testCluster))
			if test.status == Pass {
				require.NoError(t, err)
				assert.Empty(t, check.Remediation)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "aks-type")
				assert.Contains(t, err.Error(), testCluster)
				assert.Contains(t, err.Error(), "sku.name=Base")
				assert.Contains(t, err.Error(), check.Observed)
				assert.Contains(t, err.Error(), "Select an AKS Standard cluster")
			}
		})
	}
}

func TestResourceCheckerRejectsIncompleteOrMismatchedEvidence(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*readFake)
	}{
		{"missing supplied Storage", func(f *readFake) { delete(f.replies, strings.ToLower(testStorage)) }},
		{"missing supplied identity", func(f *readFake) { delete(f.replies, strings.ToLower(testHosting)) }},
		{"read forbidden", func(f *readFake) {
			f.replies[strings.ToLower(testStorage)] = readReply{403,
				map[string]any{"error": map[string]string{"code": "AuthorizationFailed", "message": "offline forbidden"}}}
			f.replies[strings.ToLower(testCluster+"/agentPools")] = readReply{403,
				map[string]any{"error": map[string]string{"code": "AuthorizationFailed", "message": "pool read forbidden"}}}
		}},
		{"different subscription tenant", func(f *readFake) {
			f.replies["/subscriptions/cluster-sub"] = readReply{200, map[string]any{"tenantId": "another-tenant"}}
		}},
		{"managedByTenants is not ownership", func(f *readFake) {
			f.replies["/subscriptions/cluster-sub"] = readReply{200,
				map[string]any{"managedByTenants": []any{map[string]any{"tenantId": "tenant"}}}}
		}},
		{"incomplete identity", func(f *readFake) {
			f.replies[strings.ToLower(testHosting)] = readReply{200, map[string]any{"id": testHosting}}
		}},
		{"different identity tenant", func(f *readFake) {
			body := f.replies[strings.ToLower(testHosting)].body.(map[string]any)
			body["properties"].(map[string]any)["tenantId"] = "another-tenant"
		}},
		{"different Storage ID", func(f *readFake) {
			f.replies[strings.ToLower(testStorage)].body.(map[string]any)["id"] = testStorage + "-other"
		}},
		{"unfinished supplied Storage", func(f *readFake) {
			body := f.replies[strings.ToLower(testStorage)].body.(map[string]any)
			body["properties"].(map[string]any)["provisioningState"] = "Updating"
		}},
		{"actual AKS version below 1.31 despite newer desired version", func(f *readFake) {
			body := f.replies[strings.ToLower(testCluster)].body.(map[string]any)
			properties := body["properties"].(map[string]any)
			properties["currentKubernetesVersion"], properties["kubernetesVersion"] = "1.30.9", "1.33.0"
		}},
		{"missing cluster managed identity", func(f *readFake) {
			body := f.replies[strings.ToLower(testCluster)].body.(map[string]any)
			delete(body, "identity")
		}},
		{"disabled workload identity", func(f *readFake) {
			body := f.replies[strings.ToLower(testCluster)].body.(map[string]any)
			body["properties"].(map[string]any)["securityProfile"] =
				map[string]any{"workloadIdentity": map[string]any{"enabled": false}}
		}},
		{"pool requirements cannot be combined across pools", func(f *readFake) {
			body := f.replies[strings.ToLower(testCluster+"/agentPools")].body.(map[string]any)
			pools := body["value"].([]any)
			properties := pools[1].(map[string]any)["properties"].(map[string]any)
			properties["nodeLabels"] = map[string]any{}
			copy := map[string]any{}
			maps.Copy(copy, properties)
			copy["nodeLabels"] = map[string]any{"agents.ai.azure.com/sandbox-node": "true"}
			copy["workloadRuntime"] = "OCIContainer"
			body["value"] = append(pools, map[string]any{
				"id": testCluster + "/agentPools/other", "name": "other", "properties": copy,
			})
		}},
		{"outdated Kata node image", func(f *readFake) {
			body := f.replies[strings.ToLower(testCluster+"/agentPools")].body.(map[string]any)
			body["value"].([]any)[1].(map[string]any)["properties"].(map[string]any)["nodeImageVersion"] =
				"AKSAzureLinux-V3katagen2-202608.13.0"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := resourceFake()
			test.change(fake)
			_, report, err := NewResourceChecker(testCredential{}, fake.options(), nil).Check(t.Context(), suppliedPlan(t))
			require.Error(t, err)
			require.Error(t, report.Err())
			if test.name == "read forbidden" {
				response, ok := errors.AsType[*azcore.ResponseError](err)
				require.True(t, ok)
				assert.Equal(t, http.StatusForbidden, response.StatusCode)
				for _, id := range []string{"system-pool", "agent-pool", "agent-image", "agent-label"} {
					check := checkWithID(t, report, id)
					assert.Equal(t, Unverified, check.Status)
					assert.Equal(t, "AKS child pools could not be read", check.Observed)
				}
				assert.Equal(t, Pass, checkWithID(t, report, "aks-version").Status)
			}
		})
	}
	subnetID := testGroup + "/providers/Microsoft.Network/virtualNetworks/network/subnets/agents"
	fake := resourceFake()
	body := map[string]any{}
	fake.replies[strings.ToLower(subnetID)] = readReply{200, body}
	checker := &sdkResourceChecker{credential: testCredential{}, options: fake.options()}
	subnet, report := checker.readNetworkSubnet(t.Context(), subnetID, "agent-subnet")
	require.Nil(t, subnet)
	require.Len(t, report, 1)
	assert.Equal(t, Unverified, report[0].Status)
	body["id"] = subnetID + "-other"
	subnet, report = checker.readNetworkSubnet(t.Context(), subnetID, "agent-subnet")
	require.Nil(t, subnet)
	require.Len(t, report, 1)
	assert.Equal(t, Fail, report[0].Status)
	assert.Equal(t, subnetID, report[0].Scope)
	body["id"] = strings.ToUpper(subnetID)
	subnet, report = checker.readNetworkSubnet(t.Context(), subnetID, "agent-subnet")
	require.NotNil(t, subnet)
	require.Empty(t, report)
}

func TestNetworkChecksPreserveDiagnosticsWithoutNodeSubnetEvidence(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*armnetwork.Subnet, *armnetwork.VirtualNetwork)
		status Status
	}{
		{"passing subnet", func(*armnetwork.Subnet, *armnetwork.VirtualNetwork) {}, Pass},
		{"missing parent address space", func(_ *armnetwork.Subnet, parent *armnetwork.VirtualNetwork) {
			parent.Properties.AddressSpace = nil
		}, Unverified},
		{"invalid subnet CIDR", func(subnet *armnetwork.Subnet, _ *armnetwork.VirtualNetwork) {
			subnet.Properties.AddressPrefix = new("invalid")
		}, Unverified},
		{"missing delegation", func(subnet *armnetwork.Subnet, _ *armnetwork.VirtualNetwork) {
			subnet.Properties.Delegations = nil
		}, Fail},
	} {
		t.Run(test.name, func(t *testing.T) {
			networkID := testGroup + "/providers/Microsoft.Network/virtualNetworks/network"
			subnetID := networkID + "/subnets/agents"
			subnet := &armnetwork.Subnet{
				ID: new(subnetID),
				Properties: &armnetwork.SubnetPropertiesFormat{
					AddressPrefix: new("10.0.1.0/27"),
					Delegations: []*armnetwork.Delegation{{
						Properties: &armnetwork.ServiceDelegationPropertiesFormat{
							ServiceName: new("Microsoft.App/environments"),
						},
					}},
				},
			}
			parent := &armnetwork.VirtualNetwork{
				ID: new(networkID),
				Properties: &armnetwork.VirtualNetworkPropertiesFormat{
					ProvisioningState: new(armnetwork.ProvisioningStateSucceeded),
					AddressSpace: &armnetwork.AddressSpace{
						AddressPrefixes: []*string{new("10.0.0.0/16")},
					},
				},
			}
			test.change(subnet, parent)
			original := delegatedSubnetCheck(subnet, parent, nil)
			require.Equal(t, test.status, original.Status)
			fake := resourceFake()
			fake.replies[strings.ToLower(subnetID)] = readReply{200, subnet}
			fake.replies[strings.ToLower(networkID)] = readReply{200, parent}
			checker := &sdkResourceChecker{credential: testCredential{}, options: fake.options()}
			plan := Plan{AgentSubnet: Resource{ID: subnetID, Supplied: true}}
			report := checker.networkChecks(t.Context(), plan, nil, nil)
			require.NotEmpty(t, report)
			check := report[len(report)-1]
			if original.Status == Pass {
				assert.Equal(t, Unverified, check.Status)
				assert.Contains(t, check.Observed, "non-overlap with the selected AKS pool Node subnet could not be verified")
				assert.NotContains(t, check.Observed, "non-overlap and")
				assert.Equal(t, "selected AKS pool Node subnet ARM CIDR metadata", check.Missing)
				assert.Contains(t, check.Remediation, "rerun preflight to verify non-overlap")
			} else {
				assert.Equal(t, original.Status, check.Status)
				assert.Equal(t, original.Observed, check.Observed)
				assert.Equal(t, original.Missing, check.Missing)
				assert.Equal(t, original.Remediation, check.Remediation)
			}
			if original.Cause != nil {
				require.Error(t, check.Cause)
				assert.EqualError(t, check.Cause, original.Cause.Error())
			} else {
				assert.NoError(t, check.Cause)
			}
			assert.True(t, check.blocks())
			assert.Error(t, report.Err())
		})
	}
}

func TestStorageRulesRetainSourceStandardsAndDefaults(t *testing.T) {
	for _, test := range []struct {
		name, property, check string
		value                 any
		status                Status
	}{
		{"HNS missing", "isHnsEnabled", "storage-type", nil, Unverified},
		{"HNS enabled", "isHnsEnabled", "storage-type", true, Fail},
		{"Cool tier", "accessTier", "storage-type", "Cool", Fail},
		{"tier missing", "accessTier", "storage-type", nil, Unverified},
		{"HTTPS missing uses true default", "supportsHttpsTrafficOnly", "storage-security", nil, Pass},
		{"HTTPS disabled", "supportsHttpsTrafficOnly", "storage-security", false, Fail},
		{"TLS missing uses 1.0 default", "minimumTlsVersion", "storage-security", nil, Fail},
		{"TLS 1.1", "minimumTlsVersion", "storage-security", "TLS1_1", Fail},
		{"TLS 1.3", "minimumTlsVersion", "storage-security", "TLS1_3", Pass},
		{"TLS unknown", "minimumTlsVersion", "storage-security", "future-value", Unverified},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := storageBody()
			properties := body["properties"].(map[string]any)
			if test.value == nil {
				delete(properties, test.property)
			} else {
				properties[test.property] = test.value
			}
			var account armstorage.Account
			raw, err := json.Marshal(body)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(raw, &account))
			report := storageResourceChecks(account)
			assert.Equal(t, test.status, checkWithID(t, report, test.check).Status)
			typeCheck := checkWithID(t, report, "storage-type")
			hns := "unavailable"
			if value, ok := properties["isHnsEnabled"]; ok {
				hns = fmt.Sprint(value)
			}
			assert.Contains(t, typeCheck.Observed, "kind=StorageV2")
			assert.Contains(t, typeCheck.Observed, "tier="+resourceValue(account.Properties.AccessTier))
			assert.Contains(t, typeCheck.Observed, "isHnsEnabled="+hns)
		})
	}
	for _, sku := range []string{"Standard_ZRS", "Standard_LRS", "unknown", ""} {
		body := storageBody()
		body["sku"] = map[string]any{"name": sku}
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		var account armstorage.Account
		require.NoError(t, json.Unmarshal(raw, &account))
		report := storageResourceChecks(account)
		require.NoError(t, report.Err(), "redundancy is a recommendation, not a blocking requirement")
		assert.Equal(t, sku != "Standard_ZRS", report[0].Warning != "")
	}
}

func TestResourceReportURLNonDisclosure(t *testing.T) {
	//nolint:gosec // G101: synthetic URL credentials exercise report and error redaction.
	report := Report{{ID: "resource-refs", Mandatory: true, Status: Fail,
		Scope:    "https://user:private-password@host/path?sig=private-token#private-fragment",
		Observed: "read https://user:private-password@host/path?sig=private-token#private-fragment"}}
	var output strings.Builder
	require.NoError(t, report.Write(&output))
	assert.NotContains(t, output.String()+report.Err().Error(), "private-")
	assert.Contains(t, output.String(), "https://host/path")
	assert.Contains(t, report.Err().Error(), "resource-refs FAIL [https://host/path]")
}
