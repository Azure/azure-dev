// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testClusterID = "/subscriptions/cluster-sub/resourceGroups/aks-rg/providers/" +
	"Microsoft.ContainerService/managedClusters/aks"
const testVNetID = "/subscriptions/network-sub/resourceGroups/network-rg/providers/Microsoft.Network/virtualNetworks/vnet"

type testCredential struct{}

func (testCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "offline-test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) Do(request *http.Request) (*http.Response, error) { return f(request) }

type clusterFixture struct {
	Cluster armcontainerservice.ManagedCluster
	Pools   []*armcontainerservice.AgentPool
	SKUs    []*armcompute.ResourceSKU
}

func fixture(t *testing.T) clusterFixture {
	t.Helper()
	var f clusterFixture
	require.NoError(t, json.Unmarshal([]byte(`{
  "Cluster": {
    "location": "westus", "identity": {"type": "SystemAssigned"}, "sku": {"name": "Base", "tier": "Free"},
    "properties": {
      "currentKubernetesVersion": "1.31.0", "provisioningState": "Succeeded",
      "networkProfile": {"networkPlugin": "azure", "networkPluginMode": "overlay", "networkDataplane": "cilium"},
      "identityProfile": {"kubeletidentity": {"objectId": "11111111-1111-4111-8111-111111111111"}},
      "oidcIssuerProfile": {"enabled": true, "issuerURL": "https://issuer.example.test/"},
      "securityProfile": {"workloadIdentity": {"enabled": true}}
    }
  },
  "Pools": [
    {"name": "system", "properties": {"mode": "System", "vmSize": "Standard_E4s_v5",
      "count": 1, "provisioningState": "Succeeded"}},
    {"name": "agents", "properties": {
      "mode": "User", "osSKU": "AzureLinux", "vmSize": "Standard_D8as_v5", "count": 1,
      "workloadRuntime": "KataVmIsolation", "nodeImageVersion": "AKSAzureLinux-V3katagen2-202608.14.0",
      "nodeLabels": {"agents.ai.azure.com/sandbox-node": "true"}, "securityProfile": {"sshAccess": "Disabled"},
      "provisioningState": "Succeeded"
    }}
  ],
  "SKUs": [
    {"name": "Standard_E4s_v5", "resourceType": "virtualMachines", "locations": ["westus"],
      "capabilities": [{"name": "vCPUs", "value": "4"}, {"name": "MemoryGB", "value": "16"}]},
    {"name": "Standard_D8as_v5", "resourceType": "virtualMachines", "locations": ["westus"],
      "capabilities": [{"name": "vCPUs", "value": "8"}, {"name": "MemoryGB", "value": "32"}]}
  ]
}`), &f))
	require.Len(t, f.Pools, 2)
	require.NotNil(t, f.Cluster.Properties)
	for _, pool := range f.Pools {
		require.NotNil(t, pool)
		require.NotNil(t, pool.Properties)
		pool.ID = new(testClusterID + "/agentPools/" + resourceValue(pool.Name))
	}
	f.Cluster.ID = new(testClusterID)
	f.Pools[1].Properties.VnetSubnetID = new(testVNetID + "/subnets/nodes")
	return f
}

func testInput(t *testing.T) Input {
	t.Helper()
	return Input{
		ClusterID: testClusterID, SubscriptionID: "target-sub",
		Network: Network{VNetID: testVNetID, AgentSubnetName: "agents", PESubnetName: "endpoints"},
	}
}

func TestNetworkRejectsLocalConflicts(t *testing.T) {
	for _, change := range []func(*Network){
		func(n *Network) { n.PESubnetName = n.AgentSubnetName },
		func(n *Network) { n.CreateAgentSubnet = true },
		func(n *Network) { n.CreateAgentSubnet, n.AgentSubnetPrefix = true, "10.0.1.1/24" },
		func(n *Network) { n.CreateAgentSubnet, n.AgentSubnetPrefix = true, "10.0.1.0/28" },
		func(n *Network) { n.CreateAgentSubnet, n.AgentSubnetPrefix = true, "8.0.0.0/8" },
		func(n *Network) { n.DNSSubscription = "11111111-1111-4111-8111-111111111111" },
	} {
		network := testInput(t).Network
		change(&network)
		require.Error(t, network.Validate())
	}
	network := testInput(t).Network
	network.CreateAgentSubnet, network.AgentSubnetPrefix = true, "10.0.1.0/24"
	network.CreatePESubnet, network.PESubnetPrefix = true, "10.0.1.128/25"
	require.ErrorContains(t, network.Validate(), "must not overlap")
	network.PESubnetPrefix = "10.0.2.0/24"
	require.NoError(t, network.Validate())
}

func TestAKSPrerequisiteRules(t *testing.T) {
	for _, test := range []struct{ name, cluster, pool string }{
		{"Automatic", `{"sku":{"name":"Automatic"}}`, `{}`},
		{"running Kubernetes version",
			`{"properties":{"currentKubernetesVersion":"1.30.99","kubernetesVersion":"1.32.0"}}`, `{}`},
		{"OIDC", `{"properties":{"oidcIssuerProfile":{"enabled":false}}}`, `{}`},
		{"CNI", `{"properties":{"networkProfile":{"networkPlugin":"kubenet"}}}`, `{}`},
		{"sandbox image", `{}`, `{"nodeImageVersion":"AKSAzureLinux-V3katagen2-202608.13.99"}`},
		{"SSH", `{}`, `{"securityProfile":{"sshAccess":"Enabled"}}`},
		{"runtime", `{}`, `{"workloadRuntime":"OCIContainer"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := fixture(t)
			require.NoError(t, json.Unmarshal([]byte(test.cluster), &f.Cluster))
			require.NoError(t, json.Unmarshal([]byte(test.pool), f.Pools[1].Properties))
			_, _, report := aksResourceChecks(f.Cluster, f.Pools, f.SKUs)
			require.Error(t, report.Err())
		})
	}
	f := fixture(t)
	principal, _, report := aksResourceChecks(f.Cluster, f.Pools, f.SKUs)
	require.NoError(t, report.Err())
	assert.Equal(t, "11111111-1111-4111-8111-111111111111", principal)
	t.Run("one pool must satisfy all requirements", func(t *testing.T) {
		f := fixture(t)
		other := fixture(t).Pools[1]
		other.Properties.Count = new(int32(0))
		f.Pools[1].Properties.NodeLabels = map[string]*string{}
		f.Pools = append(f.Pools, other)
		_, _, report := aksResourceChecks(f.Cluster, f.Pools, f.SKUs)
		require.Error(t, report.Err())
	})
}

func TestSKUCapacityExactThresholdAndLocation(t *testing.T) {
	for _, test := range []struct {
		name, patch string
		expected    Status
	}{
		{"threshold", `{}`, Pass},
		{"CPU below threshold", `{"capabilities":[{"name":"vCPUs","value":"7"},{"name":"MemoryGB","value":"32"}]}`, Fail},
		{"RAM below threshold", `{"capabilities":[{"name":"vCPUs","value":"8"},{"name":"MemoryGB","value":"31.99"}]}`, Fail},
		{"constrained CPU", `{"capabilities":[{"name":"vCPUs","value":"16"},{"name":"vCPUsAvailable","value":"4"},
			{"name":"MemoryGB","value":"64"}]}`, Fail},
		{"unrelated region", `{"locations":["eastus"]}`, Unverified},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := fixture(t)
			require.NoError(t, json.Unmarshal([]byte(test.patch), f.SKUs[1]))
			status, _ := skuCapacity(f.SKUs, "westus", "Standard_D8as_v5", 8, 32)
			assert.Equal(t, test.expected, status)
		})
	}
	f := fixture(t)
	unrelated := &armcompute.ResourceSKU{Name: new("Standard_D8as_v5"), ResourceType: new("virtualMachines"),
		Locations: []*string{new("eastus")}}
	status, _ := skuCapacity(append([]*armcompute.ResourceSKU{unrelated}, f.SKUs...), "westus", "Standard_D8as_v5", 8, 32)
	assert.Equal(t, Pass, status)
}

func testResponse(t *testing.T, request *http.Request, status int, body any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	return &http.Response{StatusCode: status, Request: request, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(bytes.NewReader(raw))}
}

func checkerOptions(t *testing.T, f clusterFixture, agentPrefix string, failCluster bool) *arm.ClientOptions {
	t.Helper()
	return &arm.ClientOptions{ClientOptions: policy.ClientOptions{
		Retry: policy.RetryOptions{MaxRetries: -1},
		Transport: testTransport(func(request *http.Request) (*http.Response, error) {
			require.Equal(t, http.MethodGet, request.Method, "preparation must never register providers or mutate Azure")
			path := request.URL.Path
			var body any
			switch {
			case path == testClusterID:
				if failCluster {
					return testResponse(t, request, 403, map[string]any{
						"error": map[string]string{"code": "AuthorizationFailed", "message": "read denied"},
					}), nil
				}
				body = f.Cluster
			case path == testClusterID+"/agentPools":
				body = map[string]any{"value": f.Pools}
			case strings.HasSuffix(path, "/providers/Microsoft.Compute/skus"):
				body = map[string]any{"value": f.SKUs}
			case strings.HasSuffix(path, "/providers/Microsoft.Authorization/roleDefinitions"):
				body = map[string]any{"value": []any{map[string]any{
					"id": "/subscriptions/target-sub/providers/Microsoft.Authorization/" +
						"roleDefinitions/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
					"properties": map[string]any{"roleName": "Managed Identity Federated Identity Credential Contributor",
						"type": "BuiltInRole"},
				}}}
			case path == testVNetID:
				body = map[string]any{"id": path, "properties": map[string]any{
					"provisioningState": "Succeeded",
					"addressSpace":      map[string]any{"addressPrefixes": []string{"10.0.0.0/16"}},
					"subnets": []any{map[string]any{"id": testVNetID + "/subnets/nodes",
						"properties": map[string]any{"addressPrefix": "10.0.0.0/24"}}},
				}}
			case strings.HasPrefix(path, testVNetID+"/subnets/"):
				prefix := "10.0.0.0/24"
				properties := map[string]any{"provisioningState": "Succeeded"}
				if strings.HasSuffix(path, "/agents") {
					prefix = agentPrefix
					properties["delegations"] = []any{map[string]any{
						"properties": map[string]string{"serviceName": "Microsoft.App/environments"},
					}}
				} else if strings.HasSuffix(path, "/endpoints") {
					prefix = "10.0.2.0/24"
				}
				properties["addressPrefix"] = prefix
				body = map[string]any{"id": path, "properties": properties}
			case strings.HasPrefix(path, "/subscriptions/") && len(strings.Split(path, "/")) == 3:
				body = map[string]any{"id": path, "tenantId": "owning-tenant"}
			default:
				t.Fatalf("unexpected prerequisite read %s", path)
			}
			return testResponse(t, request, 200, body), nil
		}),
	}}
}

func TestResourceCheckerReadOnly(t *testing.T) {
	for _, test := range []struct {
		name, prefix           string
		failCluster, wantError bool
	}{
		{"ready", "10.0.1.0/24", false, false},
		{"AKS read denied", "10.0.1.0/24", true, true},
		{"node subnet overlaps", "10.0.0.0/24", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			checker := NewResourceChecker(testCredential{}, checkerOptions(t, fixture(t), test.prefix, test.failCluster))
			facts, report, err := checker.Check(t.Context(), testInput(t))
			if test.wantError {
				require.Error(t, err)
				require.Error(t, report.Err())
				return
			}
			require.NoError(t, err)
			assert.NotEmpty(t, facts.KubeletObjectID)
			assert.Contains(t, facts.FederatedIdentityRoleDefinitionID, "/roleDefinitions/")
		})
	}
	t.Run("explicit available prefix uses standard subnet creation selection without writing", func(t *testing.T) {
		input := testInput(t)
		input.Network.CreateAgentSubnet, input.Network.AgentSubnetPrefix = true, "10.0.3.0/24"
		checker := NewResourceChecker(testCredential{}, checkerOptions(t, fixture(t), "", false))
		_, _, err := checker.Check(t.Context(), input)
		require.NoError(t, err)
	})
}

func TestDiagnosticsDoNotDiscloseURLCredentials(t *testing.T) {
	report := Report{{ID: "N02", Status: Fail, Scope: "https://user:password@host.test/path?sig=sas-secret#private",
		Observed: "read https://user:password@host.test/path?sig=sas-secret#private failed"}}
	var output bytes.Buffer
	require.NoError(t, report.Write(&output))
	require.Error(t, report.Err())
	for _, secret := range []string{"user:", "password", "sas-secret", "sig=", "#private"} {
		assert.NotContains(t, output.String(), secret)
		assert.NotContains(t, report.Err().Error(), secret)
	}
	assert.Contains(t, output.String(), "https://host.test/path")
}

func TestManagedNodeSubnetRequiresActualClusterOwnershipAndPoolEvidence(t *testing.T) {
	const nodeGroupID = "/subscriptions/cluster-sub/resourceGroups/actual-node-group"
	for _, test := range []struct {
		name, owner, pool string
		wantError         bool
	}{
		{"actual VMSS metadata", testClusterID, "agents", false},
		{"unrelated managedBy", testClusterID + "-other", "agents", true},
		{"unrelated pool", testClusterID, "other", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := fixture(t)
			f.Cluster.Properties.NodeResourceGroup = new("actual-node-group")
			f.Pools[1].Properties.VnetSubnetID = nil
			c := &sdkResourceChecker{credential: testCredential{}, options: ARMOptions(&arm.ClientOptions{
				ClientOptions: policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1},
					Transport: testTransport(func(request *http.Request) (*http.Response, error) {
						require.Equal(t, http.MethodGet, request.Method)
						switch strings.ToLower(request.URL.Path) {
						case strings.ToLower(nodeGroupID):
							return testResponse(t, request, 200,
								map[string]any{"id": nodeGroupID, "managedBy": test.owner}), nil
						case strings.ToLower(nodeGroupID + "/providers/Microsoft.Compute/virtualMachineScaleSets"):
							body := fmt.Sprintf(`{"value": [{
  "id": "%s/providers/Microsoft.Compute/virtualMachineScaleSets/unpredictable-name",
  "tags": {"aks-managed-poolName": "%s"},
  "properties": {"provisioningState": "Succeeded", "virtualMachineProfile": {"networkProfile": {
    "networkInterfaceConfigurations": [{"properties": {"ipConfigurations": [
      {"properties": {"subnet": {"id": "%s/subnets/nodes"}}}
    ]}}]
  }}}
}]}`, nodeGroupID, test.pool, testVNetID)
							return testResponse(t, request, 200, json.RawMessage(body)), nil
						default:
							t.Fatalf("unexpected managed-node discovery %s", request.URL.Path)
							return nil, nil
						}
					}),
				},
			})}
			ids, report := c.nodeSubnetIDs(t.Context(), testInput(t), &f.Cluster, f.Pools[1])
			if test.wantError {
				require.Error(t, report.Err())
				assert.Empty(t, ids)
			} else {
				require.NoError(t, report.Err())
				assert.Equal(t, []string{testVNetID + "/subnets/nodes"}, ids)
			}
		})
	}
}
