// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeploymentPlansOmitReadPermissionPrecheck(t *testing.T) {
	for _, managed := range []bool{false, true} {
		for _, create := range []bool{false, true} {
			for _, existingDNS := range []bool{false, true} {
				plan, err := BuildPlan(contractPlanInput())
				require.NoError(t, err)
				network := &LegacyNetwork{
					Enabled: true, ManagedEgress: managed, CreateAgentSubnet: create, CreatePESubnet: create,
					VNetID:          "/subscriptions/network-sub/resourceGroups/network-rg/providers/Microsoft.Network/virtualNetworks/v",
					AgentSubnetName: "agents", PESubnetName: "pe",
				}
				if existingDNS {
					network.DNSResourceGroup, network.DNSSubscription = "dns-rg", "dns-sub"
				}
				plan, err = WithDeploymentOperations(plan, DeploymentSelection{Network: network})
				require.NoError(t, err)
				options := contractARMOptions(func(r *http.Request) (*http.Response, error) {
					require.Equal(t, http.MethodGet, r.Method)
					require.Equal(t, "/subscriptions/foundry-sub/resourcegroups/new-foundry-rg",
						strings.ToLower(r.URL.Path))
					return contractARMResponse(r, http.StatusOK, `{"location":"eastus"}`), nil
				})
				plan, err = ResolveExistingParents(t.Context(), plan, contractCredential{}, options)
				require.NoError(t, err)
				for _, op := range plan.Operations {
					assert.NotEqual(t, "P08", op.CheckID)
					assert.NotEqual(t, "Microsoft.Resources/subscriptions/read", op.Action)
					assert.NotEqual(t, "Microsoft.Network/virtualNetworks/subnets/read", op.Action)
				}
			}
		}
	}
}

func TestActualResourceReadErrorsRemainMandatoryWithoutPermissionPrecheck(t *testing.T) {
	for _, adapter := range []string{"AKS", "Storage", "ACR", "subnet", "subscription", "agent pools", "SKU"} {
		for _, scenario := range []string{"forbidden", "not found", "invalid response"} {
			t.Run(adapter+"/"+scenario, func(t *testing.T) {
				plan, err := BuildPlan(contractPlanInput())
				require.NoError(t, err)
				status, code := http.StatusForbidden, "AuthorizationFailed"
				if scenario == "not found" {
					status, code = http.StatusNotFound, "ResourceNotFound"
				}
				options := contractARMOptions(func(r *http.Request) (*http.Response, error) {
					require.Equal(t, http.MethodGet, r.Method)
					assert.NotContains(t, r.URL.Path, "/providers/Microsoft.Authorization/")
					if adapter == "agent pools" || adapter == "SKU" {
						if strings.EqualFold(r.URL.Path, plan.Cluster.ID) {
							return contractARMResponse(r, http.StatusOK, fmt.Sprintf(
								`{"id":%q,"location":"eastus","properties":{"provisioningState":"Succeeded"}}`,
								plan.Cluster.ID)), nil
						}
						target := adapter == "agent pools" && strings.HasSuffix(r.URL.Path, "/agentPools") ||
							adapter == "SKU" && strings.HasSuffix(r.URL.Path, "/skus")
						if !target {
							return contractARMResponse(r, http.StatusOK, `{"value":[]}`), nil
						}
					}
					if scenario == "invalid response" {
						return contractARMResponse(r, http.StatusOK, `{"invalid":`), nil
					}
					return contractARMResponse(r, status, fmt.Sprintf(`{"error":{"code":%q}}`, code)), nil
				})
				checker := &sdkResourceChecker{credential: contractCredential{}, options: onboardingARMOptions(options)}
				var report Report
				expectedID := "I02"
				switch adapter {
				case "AKS", "agent pools", "SKU":
					_, _, _, report = checker.readCluster(t.Context(), plan.Cluster)
					if adapter == "agent pools" {
						expectedID = "A10"
					} else if adapter == "SKU" {
						expectedID = "A05"
					}
				case "Storage":
					_, report = checker.readStorage(t.Context(), plan.Storage)
				case "ACR":
					_, report = checker.readRegistry(t.Context(), plan.Registry)
					expectedID = "C01"
				case "subnet":
					_, report = checker.readNetworkSubnet(t.Context(), plan.AgentSubnet.ID, "N02")
					expectedID = "N02"
				case "subscription":
					_, report = checker.subscriptionTenants(t.Context(), plan)
				}
				require.Error(t, report.Err())
				var failures Report
				for _, check := range report {
					assert.NotEqual(t, "P08", check.ID)
					if check.ID == expectedID && check.Status != Pass {
						failures = append(failures, check)
						if scenario == "not found" {
							assert.Equal(t, Fail, check.Status)
							assert.Contains(t, check.Observed, "404")
						} else {
							assert.Equal(t, Unverified, check.Status)
							assert.NotContains(t, check.Observed, "does not exist")
						}
						if scenario != "invalid response" {
							service, ok := errors.AsType[*azcore.ResponseError](check.Cause)
							require.True(t, ok)
							assert.Equal(t, status, service.StatusCode)
							assert.Equal(t, code, service.ErrorCode)
						} else {
							require.Error(t, check.Cause)
						}
					}
				}
				require.NotEmpty(t, failures)
				pipeline := Pipeline{
					Resources: contractResources(func(context.Context, Plan) (Facts, Report, error) {
						return Facts{}, report, report.Err()
					}),
					Access: contractAccess(func(context.Context, Plan, Facts) (Report, error) { return nil, nil }),
				}
				_, _, err = pipeline.Preflight(t.Context(), plan)
				require.Error(t, err, "real read failures still block the mandatory flow")
			})
		}
	}
}

func TestParentDiscoveryDoesNotTreatDeniedReadAsAbsent(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound} {
		plan, err := BuildPlan(contractPlanInput())
		require.NoError(t, err)
		options := contractARMOptions(func(r *http.Request) (*http.Response, error) {
			require.Equal(t, http.MethodGet, r.Method)
			code := "AuthorizationFailed"
			if status == http.StatusNotFound {
				code = "ResourceGroupNotFound"
			}
			return contractARMResponse(r, status, fmt.Sprintf(`{"error":{"code":%q}}`, code)), nil
		})
		_, err = ResolveExistingParents(t.Context(), plan, contractCredential{}, options)
		if status == http.StatusNotFound {
			require.NoError(t, err, "an absent target RG can be created by the unchanged ARM graph")
		} else {
			require.Error(t, err)
			assert.Contains(t, err.Error(), "I03")
			service, ok := errors.AsType[*azcore.ResponseError](err)
			require.True(t, ok)
			assert.Equal(t, http.StatusForbidden, service.StatusCode)
		}
	}
}

func TestReadPermissionInferenceIsNotMovedToAnotherAuthorizationCheck(t *testing.T) {
	input := contractPlanInput()
	input.StorageID, input.RegistryID, input.AgentSubnetID = "", "", ""
	plan, err := BuildPlan(input)
	require.NoError(t, err)
	var requests int
	options := contractARMOptions(func(r *http.Request) (*http.Response, error) {
		requests++
		require.Equal(t, http.MethodGet, r.Method)
		var body string
		switch {
		case strings.HasSuffix(r.URL.Path, "/roleAssignments"):
			scope := strings.TrimSuffix(r.URL.Path, "/providers/Microsoft.Authorization/roleAssignments")
			body = fmt.Sprintf(`{"value":[{"properties":{"scope":%q,"principalId":"operator","principalType":"User",
				"roleDefinitionId":"/providers/Microsoft.Authorization/roleDefinitions/custom"}}]}`, scope)
		case strings.Contains(r.URL.Path, "/roleDefinitions/"):
			body = `{"properties":{"permissions":[{"actions":["*"],"notActions":[
				"Microsoft.Resources/subscriptions/read","Microsoft.Resources/subscriptions/resourceGroups/read",
				"Microsoft.ContainerService/managedClusters/read","Microsoft.ContainerService/managedClusters/agentPools/read",
				"Microsoft.Compute/skus/read","Microsoft.Storage/storageAccounts/read",
				"Microsoft.ContainerRegistry/registries/read","Microsoft.Network/virtualNetworks/subnets/read"]}]}}`
		default:
			t.Fatalf("unexpected authorization request: %s", r.URL.Path)
		}
		return contractARMResponse(r, http.StatusOK, body), nil
	})
	report, err := NewAuthorizer(removedChecksCredential{}, options).Check(t.Context(), plan,
		Facts{KubeletObjectID: "kubelet"})
	require.NoError(t, err, "missing P08 read actions must not block other authorized operations")
	require.Len(t, report, len(plan.Operations)+3)
	assert.Equal(t, 2*len(report), requests,
		"each basic action check reads only role assignments and definitions")
	seen := map[string]bool{}
	for _, check := range report {
		assert.NotEqual(t, "P08", check.ID)
		assert.Equal(t, Pass, check.Status, check.Observed)
		seen[check.ID] = true
	}
	assert.True(t, seen["P06"])
	assert.True(t, seen["P07"])
	assert.False(t, seen["P09"], "ordinary owned RBAC registry requires no extra F01 precheck")
}
