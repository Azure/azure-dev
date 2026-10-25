// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"azure.ai.projects/internal/onboarding"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBYOP09RegistrySelectionSurvivesEnvironmentOutputOnRetry(t *testing.T) {
	p := byoContractProvider(t)
	var events []string
	p.byoPipeline = byoContractPipeline(&events, onboarding.Pass)
	var first onboarding.Plan
	p.byoPipeline.Access = byoAccessContract(func(
		_ context.Context, plan onboarding.Plan, _ onboarding.Facts,
	) (onboarding.Report, error) {
		first = plan
		assert.False(t, plan.RegistryCustomerSupplied)
		return nil, nil
	})
	require.NoError(t, p.planBYO(t.Context(), func(string) {}, true))
	state := onboarding.PreparationState{
		Version: 2, Owner: first.Owner, SubscriptionID: first.SubscriptionID,
		ResourceGroup: first.ResourceGroup, Location: first.Location,
		Resources: map[string]onboarding.OwnedResource{"R03": {ID: first.Registry.ID}},
	}
	raw, err := json.Marshal(state)
	require.NoError(t, err)
	p.virtualEnv = map[string]string{
		"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": first.Registry.ID, envKeyBYOJournal: string(raw),
	}
	for _, customer := range []bool{false, true} {
		if customer {
			p.virtualEnv["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = strings.Replace(first.Registry.ID,
				first.Registry.Name, "customer", 1)
		}
		p.byoPipeline.Access = byoAccessContract(func(
			_ context.Context, plan onboarding.Plan, _ onboarding.Facts,
		) (onboarding.Report, error) {
			assert.Equal(t, customer, plan.RegistryCustomerSupplied)
			assert.True(t, plan.Registry.Supplied, "reference selection is independent of P09 provenance")
			for _, op := range plan.Operations {
				if op.CheckID == "P09" {
					assert.Equal(t, "/subscriptions/aks-sub/resourceGroups/aks-rg", op.Scope)
					assert.True(t, strings.HasPrefix(op.Action, "Microsoft.Resources/deployments/"))
				}
			}
			return nil, nil
		})
		require.NoError(t, p.planBYO(t.Context(), func(string) {}, true))
	}
	assert.Equal(t, string(raw), p.virtualEnv[envKeyBYOJournal], "preflight never rewrites ownership")
}
