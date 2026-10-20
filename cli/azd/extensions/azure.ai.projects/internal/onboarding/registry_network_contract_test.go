// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func registryPathEvidence(request NetworkRequest) NetworkEvidence {
	return NetworkEvidence{
		SourceSubnetIDs: request.SourceSubnetIDs, Destination: request.Destination,
		Description: "offline source-bound path evidence", ExistingWorkload: true,
		DNSVerified: true, TCP443Verified: true, TLSVerified: true,
		EffectiveRoutesVerified: true, EffectivePolicyVerified: true, StorageServiceTagCoverage: true,
		PrivateEndpointDNSVerified: true,
	}
}

func TestPreparedRegistryRequiresEveryActualEndpointAndTLS(t *testing.T) {
	for _, scenario := range []string{
		"reachable", "missing provider", "missing dedicated endpoints", "unreachable login", "unreachable data",
		"missing TLS", "wrong source", "private DNS missing",
	} {
		t.Run(scenario, func(t *testing.T) {
			plan, err := BuildPlan(contractPlanInput())
			require.NoError(t, err)
			var destinations []string
			options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, request.Method)
				require.Equal(t, plan.Registry.ID, request.URL.Path)
				require.Equal(t, "2025-03-01-preview", request.URL.Query().Get("api-version"))
				hosts := `["customer.westus.data.azurecr.io","customer.eastus.data.azurecr.io"]`
				if scenario == "missing dedicated endpoints" {
					hosts = "[]"
				}
				return contractARMResponse(request, 200, `{"id":"`+plan.Registry.ID+`","properties":{
					"provisioningState":"Succeeded","loginServer":"customer.azurecr.io",
					"publicNetworkAccess":"Disabled","dataEndpointEnabled":true,"dataEndpointHostNames":`+hosts+`}}`), nil
			})
			checker := &sdkResourceChecker{credential: contractCredential{}, options: onboardingARMOptions(options)}
			if scenario != "missing provider" {
				checker.evidence.Network = networkEvidenceContract(func(_ context.Context, request NetworkRequest) (NetworkEvidence, error) {
					require.Equal(t, "R06", request.CheckID)
					destinations = append(destinations, request.Destination)
					evidence := registryPathEvidence(request)
					switch scenario {
					case "unreachable login":
						evidence.ConnectivityFailed = request.Destination == "https://customer.azurecr.io"
					case "unreachable data":
						evidence.ConnectivityFailed = strings.Contains(request.Destination, ".eastus.data.")
					case "missing TLS":
						evidence.TLSVerified = false
					case "wrong source":
						evidence.SourceSubnetIDs = []string{"another-pool"}
					case "private DNS missing":
						evidence.PrivateEndpointDNSVerified = false
					}
					return evidence, nil
				})
			}
			report := checker.registryNetworkChecks(t.Context(), plan.Registry.ID, []string{"qualified-pool-source"})
			if scenario == "reachable" {
				require.NoError(t, report.Err())
				assert.Equal(t, []string{
					"https://customer.azurecr.io", "https://customer.westus.data.azurecr.io",
					"https://customer.eastus.data.azurecr.io",
				}, destinations)
				for _, check := range report {
					assert.Contains(t, check.Observed, "authorization and propagation are separate")
				}
			} else {
				require.Error(t, report.Err())
			}
		})
	}
}

func installPreparedNetworkFixture(t *testing.T, f *preparationARM) *arm.ClientOptions {
	t.Helper()
	fixture := newResourceRuleContractFixture(t)
	fixture.Cluster.ID = new(f.plan.Cluster.ID)
	fixture.Cluster.Properties.NetworkProfile.ServiceCidr = new("10.3.0.0/16")
	fixture.Cluster.Properties.NetworkProfile.PodCidr = new("10.244.0.0/16")
	vnetID := strings.TrimSuffix(f.plan.AgentSubnet.ID, "/subnets/agents")
	source := vnetID + "/subnets/nodes"
	fixture.Pools[1].Properties.VnetSubnetID = new(source)
	add := func(id string, value any) {
		data, err := json.Marshal(value)
		require.NoError(t, err)
		var body map[string]any
		require.NoError(t, json.Unmarshal(data, &body))
		f.resources[strings.ToLower(id)] = body
	}
	add(f.plan.Cluster.ID, fixture.Cluster)
	add(f.plan.Cluster.ID+"/agentPools", map[string]any{"value": fixture.Pools})
	add("/subscriptions/aks-sub/providers/Microsoft.Compute/skus", map[string]any{"value": fixture.SKUs})
	node := map[string]any{"id": source, "properties": map[string]any{
		"provisioningState": "Succeeded", "addressPrefix": "10.0.0.0/24",
	}}
	agent := map[string]any{"id": f.plan.AgentSubnet.ID, "properties": map[string]any{
		"provisioningState": "Succeeded", "addressPrefix": "10.0.1.0/27",
		"delegations": []any{map[string]any{"properties": map[string]any{"serviceName": "Microsoft.App/environments"}}},
	}}
	add(source, node)
	add(f.plan.AgentSubnet.ID, agent)
	add(vnetID, map[string]any{"id": vnetID, "properties": map[string]any{
		"provisioningState": "Succeeded", "addressSpace": map[string]any{"addressPrefixes": []string{"10.0.0.0/16"}},
		"subnets": []any{node, agent},
	}})
	options := f.options()
	transport := options.Transport
	options.Transport = contractTransport(func(request *http.Request) (*http.Response, error) {
		response, err := transport.Do(request)
		if err != nil || request.Method != http.MethodGet || response.StatusCode != http.StatusOK ||
			!strings.EqualFold(request.URL.Path, f.plan.Storage.ID) {
			return response, err
		}
		var body map[string]any
		require.NoError(t, json.NewDecoder(response.Body).Decode(&body))
		require.NoError(t, response.Body.Close())
		properties, ok := body["properties"].(map[string]any)
		require.True(t, ok)
		properties["primaryEndpoints"] = map[string]any{"blob": "https://storage.blob.core.windows.net/"}
		properties["publicNetworkAccess"] = "Enabled"
		properties["networkAcls"] = map[string]any{"defaultAction": "Allow"}
		data, err := json.Marshal(body)
		require.NoError(t, err)
		response.Body = io.NopCloser(strings.NewReader(string(data)))
		return response, nil
	})
	return options
}

func TestUnreachablePreparedRegistryStopsFoundryForEveryResourceCombination(t *testing.T) {
	for _, storage := range []bool{false, true} {
		for _, registry := range []bool{false, true} {
			t.Run(fmt.Sprintf("supplied-storage=%t/supplied-registry=%t", storage, registry), func(t *testing.T) {
				f, facts := preparationContractFixture(t, storage, registry, false)
				options := installPreparedNetworkFixture(t, f)
				var registryRequests int
				checker := &sdkResourceChecker{
					credential: contractCredential{}, options: onboardingARMOptions(options),
					evidence: ResourceEvidence{Network: networkEvidenceContract(
						func(_ context.Context, request NetworkRequest) (NetworkEvidence, error) {
							evidence := registryPathEvidence(request)
							if request.CheckID == "R06" {
								registryRequests++
								evidence.ConnectivityFailed = true
							}
							return evidence, nil
						})},
				}
				preparer := &ownedPreparer{credential: contractCredential{}, options: onboardingARMOptions(options),
					journal: f.journal, network: checker}
				pipeline := Pipeline{
					Resources: contractResources(func(context.Context, Plan) (Facts, Report, error) {
						return facts, nil, nil
					}),
					Access:  contractAccess(func(context.Context, Plan, Facts) (Report, error) { return nil, nil }),
					Prepare: preparer,
				}
				_, err := pipeline.Run(t.Context(), f.plan, false, func(context.Context, Prepared) error {
					t.Fatal("unreachable actual registry must prevent Foundry deployment")
					return nil
				})
				require.Error(t, err)
				assert.Contains(t, err.Error(), "R06")
				assert.Positive(t, registryRequests, "actual post-CRUD registry evidence must run")
				state, loadErr := f.journal.Load(t.Context())
				require.NoError(t, loadErr)
				assert.NotEmpty(t, state.Resources, "retain ownership state after a prepared-path failure")
				if storage {
					assert.NotContains(t, f.writes, f.plan.Storage.ID)
				}
				if registry {
					assert.NotContains(t, f.writes, f.plan.Registry.ID)
				}
			})
		}
	}
}
