// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/onboarding"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
)

func (p *FoundryProvisioningProvider) byoJournalValue(ctx context.Context) (string, error) {
	if value, present := p.virtualEnv[envKeyBYOJournal]; present && value != "" {
		return value, nil
	}
	if p.azdClient == nil {
		return "", nil
	}
	response, err := p.azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{Name: p.envName})
	if err != nil {
		return "", err
	}
	for _, value := range response.KeyValues {
		if value.Key == envKeyBYOJournal {
			return value.Value, nil
		}
	}
	return "", nil
}

func (p *FoundryProvisioningProvider) destroyBYO(
	ctx context.Context, options *azdext.ProvisioningDestroyOptions, progress grpcbroker.ProgressFunc,
) (*azdext.ProvisioningDestroyResult, bool, error) {
	raw, err := p.byoJournalValue(ctx)
	if err != nil {
		return nil, true, p.byoLifecycleError(fmt.Errorf("read ownership journal before destroy: %w", err),
			exterrors.OpBYOOwnedCleanup)
	}
	currentBYO := p.synthResult != nil && p.synthResult.Hosting != nil
	if raw == "" && !currentBYO {
		return nil, false, nil
	}
	fail := func(err error) (*azdext.ProvisioningDestroyResult, bool, error) {
		return nil, true, p.byoLifecycleError(err, exterrors.OpBYOOwnedCleanup)
	}
	if raw == "" {
		return fail(fmt.Errorf("BYO-AKS ownership journal is absent; refusing both dependency and resource-group deletion"))
	}
	var state onboarding.PreparationState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return fail(fmt.Errorf("ownership journal is invalid; retain it for recovery: %w", err))
	}
	plan, err := onboarding.CleanupPlan(state)
	if err != nil {
		return fail(err)
	}
	if !options.GetForce() {
		confirmed, err := p.confirmDestroyTarget(ctx,
			"only journaled owned Foundry resources and grants; resource groups and supplied resources will be retained")
		if err != nil {
			return nil, true, err
		}
		if !confirmed {
			return nil, true, exterrors.Cancelled("owned BYO-AKS cleanup cancelled")
		}
	}
	lifecycle := p.byoLifecycle
	if lifecycle == nil {
		target := *p
		target.subID = plan.SubscriptionID
		if p.azdClient != nil {
			target.credential = nil
		}
		if err := target.ensureCredential(ctx); err != nil {
			return fail(err)
		}
		lifecycle = onboarding.NewLifecycle(target.credential, p.armClientOptions, environmentJournal{p})
	}
	if err := lifecycle.Cleanup(ctx, plan, options.GetPurge(), progress); err != nil {
		return fail(err)
	}
	result := invalidatedEnvKeysResult()
	_, registryOwned := state.Resources["R03"]
	result.InvalidatedEnvKeys = slices.DeleteFunc(result.InvalidatedEnvKeys, func(key string) bool {
		return key == envKeyResourceGroup || key == envKeyFoundryRG || key == envKeyFoundryRGOwner ||
			key == envKeyBYOJournal || (!registryOwned &&
			(key == "AZURE_CONTAINER_REGISTRY_RESOURCE_ID" || key == "AZURE_CONTAINER_REGISTRY_ENDPOINT"))
	})
	return result, true, nil
}
