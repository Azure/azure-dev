// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// ResolveRecoveryContext validates old attempts before supplied-identity permission checks.
// It is read-only, including in preview, and does not infer ownership from a tag alone.
func ResolveRecoveryContext(
	ctx context.Context, plan Plan, credential azcore.TokenCredential, options *arm.ClientOptions, journal Journal,
) (Plan, error) {
	lifecycle := &ownedLifecycle{credential: credential, options: onboardingARMOptions(options), journal: journal}
	state, err := lifecycle.load(ctx, plan)
	if err != nil {
		return plan, err
	}
	if err := validateProgressSelection(plan, state); err != nil {
		return plan, err
	}
	if state.CleanupComplete {
		return plan, nil
	}
	if state.CleanupStarted {
		return plan, fmt.Errorf("owned cleanup is incomplete; resume azd down before provisioning")
	}
	if err := validateHistoricalNetwork(state); err != nil {
		return plan, err
	}
	if state.Foundry == nil {
		return plan, nil
	}
	data := state.Foundry.Spec.Deployment
	if data == nil || !strings.EqualFold(data.ClusterID, plan.Cluster.ID) ||
		!strings.EqualFold(data.Hosting.ID, plan.Hosting.ID) || data.Hosting.Supplied != plan.Hosting.Supplied ||
		!strings.EqualFold(data.Workload.ID, plan.Workload.ID) || data.Workload.Supplied != plan.Workload.Supplied ||
		!strings.EqualFold(data.StorageID, plan.Storage.ID) || !strings.EqualFold(data.RegistryID, plan.Registry.ID) {
		return plan, fmt.Errorf("resource selections differ from the exact active ARM preparation intent")
	}
	_, ownedStorage := state.Resources["R02"]
	storageCustomerSupplied := data.StorageSupplied && !ownedStorage
	if storageCustomerSupplied != plan.Storage.Supplied {
		return plan, fmt.Errorf("Storage source differs from the exact active ARM preparation intent")
	}
	if err := lifecycle.verifyAttempts(ctx, plan, state.Foundry); err != nil {
		return plan, err
	}
	succeeded := map[string]bool{}
	if _, err := lifecycle.deploymentGrantTargets(ctx, plan, state.Foundry, succeeded); err != nil {
		return plan, err
	}
	everSucceeded := false
	for _, attempt := range state.Foundry.Attempts {
		everSucceeded = everSucceeded || attempt.ProvisioningState == "Succeeded"
	}
	pending := func(key, scope string) bool {
		if everSucceeded || state.ResourceProgress[key].PreparationComplete {
			return false
		}
		for _, assignment := range state.Assignments {
			if assignment.Complete && strings.EqualFold(assignment.Scope, scope) {
				return false
			}
		}
		for target := range succeeded {
			if id, err := ParseID(target, "Microsoft.Authorization/roleAssignments"); err == nil &&
				strings.EqualFold(id.Parent.String(), scope) {
				return false
			}
		}
		return true
	}
	if !plan.Workload.Supplied {
		exists, err := lifecycle.existingWorkload(ctx, plan, state)
		if err != nil {
			return plan, err
		}
		plan.Recovery.ExistingWorkload = exists
		plan.Recovery.FederationPending = plan.Hosting.Supplied && exists && pending("workloadIdentity", plan.Workload.ID)
	}
	if !plan.Storage.Supplied {
		client, err := armresources.NewClient(plan.SubscriptionID, credential, lifecycle.options)
		if err != nil {
			return plan, err
		}
		found, err := client.GetByID(ctx, plan.Storage.ID, "2024-01-01", nil)
		if err != nil && !responseNotFound(err) {
			return plan, fmt.Errorf("read exact intended Storage dependency: %w", err)
		}
		if err == nil {
			if err := confirmOwned(state, "R02", authString(found.ID), found.Tags); err != nil {
				return plan, err
			}
			plan.Recovery.ExistingStorage = true
			plan.Operations = slices.DeleteFunc(slices.Clone(plan.Operations), func(operation Operation) bool {
				return operation.CheckID == "P04" && operation.Action == "Microsoft.Storage/storageAccounts/write"
			})
			plan.Recovery.StorageGrantPending = plan.Workload.Supplied && pending("storageAccount", plan.Storage.ID)
		}
	}
	return plan, nil
}

func (l *ownedLifecycle) existingWorkload(ctx context.Context, plan Plan, state PreparationState) (bool, error) {
	client, err := armmsi.NewUserAssignedIdentitiesClient(plan.SubscriptionID, l.credential, l.options)
	if err != nil {
		return false, err
	}
	pager := client.NewListByResourceGroupPager(plan.ResourceGroup, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if responseNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("read existing intended workload dependency: %w", err)
		}
		for _, identity := range page.Value {
			if identity == nil {
				return false, fmt.Errorf("intended workload dependency listing is incomplete")
			}
			if !strings.EqualFold(authString(identity.ID), plan.Workload.ID) {
				continue
			}
			if err := confirmOwned(state, "R05", authString(identity.ID), identity.Tags); err != nil {
				return false, err
			}
			if err := verifyOwnedIdentity(state, "R05", *identity); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	return false, nil
}
