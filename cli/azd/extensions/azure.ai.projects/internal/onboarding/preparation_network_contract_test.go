// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type networkARMContract struct {
	t            *testing.T
	plan         Plan
	fixture      resourceRuleContractFixture
	vnet         armnetwork.VirtualNetwork
	subnet       map[string]any
	journal      *memoryPreparationJournal
	methods      []string
	writes       int
	failRead     bool
	failAfterPut bool
}

func newNetworkARMContract(t *testing.T) *networkARMContract {
	t.Helper()
	input := contractPlanInput()
	input.AgentSubnetID = ""
	input.Location = "westus"
	plan, err := BuildPlan(input)
	require.NoError(t, err)
	fixture := newResourceRuleContractFixture(t)
	fixture.Cluster.ID = new(plan.Cluster.ID)
	fixture.Cluster.Properties.NetworkProfile.ServiceCidr = new("10.3.0.0/16")
	fixture.Cluster.Properties.NetworkProfile.PodCidr = new("10.244.0.0/16")
	const vnetID = "/subscriptions/aks-sub/resourceGroups/aks-rg/providers/Microsoft.Network/virtualNetworks/aks"
	fixture.Pools[1].Properties.VnetSubnetID = new(vnetID + "/subnets/nodes")
	fixture.Pools[1].ID = new(plan.Cluster.ID + "/agentPools/agents")
	return &networkARMContract{
		t: t, plan: plan, fixture: fixture, journal: &memoryPreparationJournal{},
		vnet: armnetwork.VirtualNetwork{
			ID: new(vnetID), Location: new("westus"),
			Properties: &armnetwork.VirtualNetworkPropertiesFormat{
				ProvisioningState: new(armnetwork.ProvisioningStateSucceeded),
				AddressSpace:      &armnetwork.AddressSpace{AddressPrefixes: []*string{new("10.0.0.0/16")}},
				Subnets: []*armnetwork.Subnet{{
					ID: new(vnetID + "/subnets/nodes"), Name: new("nodes"),
					Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: new("10.0.0.0/24")},
				}},
			},
		},
	}
}

func (f *networkARMContract) options() *arm.ClientOptions {
	return contractARMOptions(func(request *http.Request) (*http.Response, error) {
		f.methods = append(f.methods, request.Method+" "+request.URL.Path)
		respond := func(status int, body any) (*http.Response, error) {
			data, err := json.Marshal(body)
			require.NoError(f.t, err)
			return contractARMResponse(request, status, string(data)), nil
		}
		path := strings.ToLower(request.URL.Path)
		switch {
		case strings.HasSuffix(path, "/agentpools"):
			return respond(200, map[string]any{"value": f.fixture.Pools})
		case strings.Contains(path, "/providers/microsoft.compute/skus"):
			return respond(200, map[string]any{"value": f.fixture.SKUs})
		case strings.EqualFold(request.URL.Path, f.plan.Cluster.ID):
			return respond(200, f.fixture.Cluster)
		case strings.EqualFold(request.URL.Path, resourceValue(f.vnet.ID)):
			parent := f.vnet
			props := *f.vnet.Properties
			parent.Properties = &props
			if f.subnet != nil {
				var child armnetwork.Subnet
				data, err := json.Marshal(f.subnet)
				require.NoError(f.t, err)
				require.NoError(f.t, json.Unmarshal(data, &child))
				props.Subnets = append(append([]*armnetwork.Subnet{}, props.Subnets...), &child)
			}
			return respond(200, parent)
		case strings.Contains(path, "/subnets/azd-agents-"):
			require.NotEmpty(f.t, request.URL.Query().Get("api-version"))
			switch request.Method {
			case http.MethodGet:
				if f.failRead {
					f.failRead = false
					return respond(500, map[string]any{"error": map[string]string{"code": "ReadbackInterrupted"}})
				}
				if f.subnet == nil {
					return respond(404, map[string]any{"error": map[string]string{"code": "ResourceNotFound"}})
				}
				return respond(200, f.subnet)
			case http.MethodPut:
				state, err := f.journal.Load(f.t.Context())
				require.NoError(f.t, err)
				require.NotNil(f.t, state.Network)
				assert.Equal(f.t, request.URL.Path, state.Resources["R06Subnet"].ID)
				f.writes++
				var body map[string]any
				require.NoError(f.t, json.NewDecoder(request.Body).Decode(&body))
				assert.NotContains(f.t, body, "tags", "Subnet ARM resources do not support owner tags")
				properties := body["properties"].(map[string]any)
				assert.Equal(f.t, "10.0.1.0/27", properties["addressPrefix"])
				properties["provisioningState"] = "Succeeded"
				body["id"] = request.URL.Path
				f.subnet = body
				if f.failAfterPut {
					f.failRead = true
				}
				return respond(200, body)
			}
		}
		f.t.Fatalf("unexpected default topology request: %s %s", request.Method, request.URL.Path)
		return nil, nil
	})
}

func (f *networkARMContract) resolve() Plan {
	f.t.Helper()
	plan, err := ResolveDefaultNetwork(f.t.Context(), f.plan, contractCredential{}, f.options(), f.journal)
	require.NoError(f.t, err)
	require.NotNil(f.t, plan.Network)
	return plan
}

func TestDefaultNetworkSDKPlanAndJournaledPreparation(t *testing.T) {
	f := newNetworkARMContract(t)
	plan := f.resolve()
	assert.False(t, plan.AgentSubnet.Supplied)
	assert.Equal(t, "10.0.1.0/27", plan.Network.AddressPrefix)
	assert.Equal(t, 0, f.writes)
	for _, method := range f.methods {
		assert.True(t, strings.HasPrefix(method, "GET "))
	}
	var selectedWrite bool
	for _, op := range plan.Operations {
		if op.Action == "Microsoft.Network/virtualNetworks/subnets/write" {
			selectedWrite = true
			assert.Equal(t, plan.Network.VNetID, op.Scope)
		}
	}
	assert.True(t, selectedWrite)
	p := &ownedPreparer{credential: contractCredential{}, options: onboardingARMOptions(f.options()), journal: f.journal}
	state, err := p.beginState(t.Context(), plan)
	require.NoError(t, err)
	require.NoError(t, p.prepareNetwork(t.Context(), plan, &state))
	assert.Equal(t, 1, f.writes)
	assert.True(t, state.Resources["R06Subnet"].Complete)
	resumed := f.resolve()
	assert.Equal(t, plan.Network, resumed.Network)
	state, err = p.beginState(t.Context(), resumed)
	require.NoError(t, err)
	require.NoError(t, p.prepareNetwork(t.Context(), resumed, &state))
	assert.Equal(t, 1, f.writes, "completed subnet is reused without rewriting customer networks")
}

func TestDefaultNetworkInterruptedReadbackResumesExactOwnedSubnet(t *testing.T) {
	f := newNetworkARMContract(t)
	f.failAfterPut = true
	plan := f.resolve()
	p := &ownedPreparer{credential: contractCredential{}, options: onboardingARMOptions(f.options()), journal: f.journal}
	state, err := p.beginState(t.Context(), plan)
	require.NoError(t, err)
	require.ErrorContains(t, p.prepareNetwork(t.Context(), plan, &state), "ReadbackInterrupted")
	assert.False(t, state.Resources["R06Subnet"].Complete)
	resumed := f.resolve()
	state, err = p.beginState(t.Context(), resumed)
	require.NoError(t, err)
	require.NoError(t, p.prepareNetwork(t.Context(), resumed, &state))
	assert.Equal(t, 1, f.writes)
}

func TestDefaultNetworkRejectsCollisionDriftAndCancellation(t *testing.T) {
	for _, scenario := range []string{"untracked resource", "CIDR occupied", "cancellation", "completed disappearance"} {
		t.Run(scenario, func(t *testing.T) {
			f := newNetworkARMContract(t)
			plan := f.resolve()
			p := &ownedPreparer{credential: contractCredential{}, options: onboardingARMOptions(f.options()), journal: f.journal}
			state, err := p.beginState(t.Context(), plan)
			require.NoError(t, err)
			ctx := t.Context()
			switch scenario {
			case "untracked resource":
				f.subnet = map[string]any{"id": plan.AgentSubnet.ID}
			case "CIDR occupied":
				f.vnet.Properties.Subnets = append(f.vnet.Properties.Subnets, &armnetwork.Subnet{
					ID:         new(plan.Network.VNetID + "/subnets/customer"),
					Properties: &armnetwork.SubnetPropertiesFormat{AddressPrefix: new(plan.Network.AddressPrefix)},
				})
			case "cancellation":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			case "completed disappearance":
				state.Resources["R06Subnet"] = OwnedResource{ID: plan.AgentSubnet.ID, Complete: true}
				require.NoError(t, f.journal.Save(ctx, state))
			}
			err = p.prepareNetwork(ctx, plan, &state)
			require.Error(t, err, fmt.Sprint(scenario))
			assert.Equal(t, 0, f.writes)
		})
	}
}
