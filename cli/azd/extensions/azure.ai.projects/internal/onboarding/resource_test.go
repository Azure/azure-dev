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
	plan, err := BuildPlan(Input{
		SubscriptionID: "target-sub", ResourceGroup: "target-rg", GroupExists: true,
		ClusterID: testCluster, StorageID: testStorage, HostingIdentityID: testHosting, WorkloadIdentityID: testWorkload,
	})
	require.NoError(t, err)
	return plan
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
		"id": testCluster, "location": "westus", "sku": map[string]any{"name": "Base"},
		"identity": map[string]any{"type": "SystemAssigned"},
		"properties": map[string]any{
			"currentKubernetesVersion": "1.31.0", "provisioningState": "Succeeded",
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
			"mode": "System", "vmSize": "Standard_E4s_v5", "count": 1, "provisioningState": "Succeeded",
		}},
		map[string]any{"id": testCluster + "/agentPools/agents", "name": "agents", "properties": map[string]any{
			"mode": "User", "osSKU": "AzureLinux", "vmSize": "Standard_D8as_v5", "count": 1,
			"workloadRuntime": "KataVmIsolation", "nodeImageVersion": "AKSAzureLinux-V3katagen2-202608.14.0",
			"nodeLabels":      map[string]any{"agents.ai.azure.com/sandbox-node": "true"},
			"securityProfile": map[string]any{"sshAccess": "Disabled"}, "provisioningState": "Succeeded",
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

func TestBuildPlanIndependentSelectionsAndExistingParents(t *testing.T) {
	for mask := range 8 {
		for _, groupExists := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/group=%t", mask, groupExists), func(t *testing.T) {
				input := Input{
					SubscriptionID: "target-sub", ResourceGroup: "target-rg", GroupExists: groupExists,
					ClusterID: testCluster, DefaultStorageID: testStorage, DefaultHostingID: testHosting,
					DefaultWorkloadID: testWorkload,
				}
				for i, field := range []*string{&input.StorageID, &input.HostingIdentityID, &input.WorkloadIdentityID} {
					if mask&(1<<i) != 0 {
						*field = []string{testStorage, testHosting, testWorkload}[i]
					}
				}
				plan, err := BuildPlan(input)
				require.NoError(t, err)
				for i, resource := range []Resource{plan.Storage, plan.Hosting, plan.Workload} {
					assert.Equal(t, mask&(1<<i) != 0, resource.Supplied)
					expected := testSubscription
					if groupExists || resource.Supplied {
						expected = testGroup
					}
					assert.Equal(t, expected, resource.Parent)
				}
			})
		}
	}
	for _, invalid := range []string{testStorage + "/child/name", testHosting, "https://user:private@host?sig=private"} {
		_, err := BuildPlan(Input{
			SubscriptionID: "target-sub", ResourceGroup: "target-rg", ClusterID: testCluster,
			StorageID: invalid, HostingIdentityID: testHosting, WorkloadIdentityID: testWorkload,
		})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "private")
	}
}

func TestResourceCheckerReadsOnlySupportingResources(t *testing.T) {
	fake := resourceFake()
	facts, report, err := NewResourceChecker(testCredential{}, fake.options()).Check(t.Context(), suppliedPlan(t))
	require.NoError(t, err)
	assert.Equal(t, "tenant", facts.TenantID)
	assert.Equal(t, "hosting-principal", facts.HostingIdentity.PrincipalID)
	assert.Equal(t, "workload-principal", facts.WorkloadIdentity.PrincipalID)
	for _, resource := range []Resource{{ID: testStorage}, {ID: testHosting}, {ID: testWorkload}} {
		assert.True(t, facts.exists(resource))
		assert.False(t, facts.absent(resource))
	}
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
			_, report, err := NewResourceChecker(testCredential{}, fake.options()).Check(t.Context(), suppliedPlan(t))
			require.Error(t, err)
			require.Error(t, report.Err())
			if test.name == "read forbidden" {
				response, ok := errors.AsType[*azcore.ResponseError](err)
				require.True(t, ok)
				assert.Equal(t, http.StatusForbidden, response.StatusCode)
			}
		})
	}
}

func TestDefaultExistenceDoesNotBecomeSupplied(t *testing.T) {
	for _, state := range []string{"absent", "existing", "forbidden"} {
		t.Run(state, func(t *testing.T) {
			fake := resourceFake()
			plan := suppliedPlan(t)
			plan.Storage.Supplied = false
			switch state {
			case "absent":
				delete(fake.replies, strings.ToLower(testStorage))
			case "existing":
				body := fake.replies[strings.ToLower(testStorage)].body.(map[string]any)
				body["properties"] = map[string]any{}
			case "forbidden":
				fake.replies[strings.ToLower(testStorage)] = readReply{403,
					map[string]any{"error": map[string]string{"code": "AuthorizationFailed"}}}
			}
			facts, report, err := NewResourceChecker(testCredential{}, fake.options()).Check(t.Context(), plan)
			assert.False(t, plan.Storage.Supplied)
			assert.Equal(t, state == "absent", facts.absent(plan.Storage))
			assert.Equal(t, state == "existing", facts.exists(plan.Storage))
			if state == "forbidden" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			for _, id := range []string{"storage-type", "storage-security"} {
				assert.Equal(t, NotApplicable, checkWithID(t, report, id).Status)
			}
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
			assert.Equal(t, test.status, checkWithID(t, storageResourceChecks(account), test.check).Status)
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
}
