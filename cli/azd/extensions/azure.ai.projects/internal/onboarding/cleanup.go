// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

func (l *ownedLifecycle) Cleanup(ctx context.Context, plan Plan, purge bool, progress func(string)) error {
	state, err := l.load(ctx, plan)
	if err != nil {
		return err
	}
	if err := validateCleanupLedger(plan, state); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	wasComplete := state.CleanupComplete
	state.CleanupStarted = true
	state.CleanupComplete = false
	if err := l.journal.Save(ctx, state); err != nil {
		return fmt.Errorf("persist cleanup intent: %w", err)
	}
	if state.Foundry != nil && state.Foundry.Spec.Deployment != nil && !wasComplete {
		if err := l.verifyAttempts(ctx, plan, state.Foundry); err != nil {
			return err
		}
		if err := l.observeDeployment(ctx, plan, &state); err != nil {
			return fmt.Errorf("recover ARM graph ownership before cleanup: %w", err)
		}
	}
	if !wasComplete && state.Foundry != nil {
		if err := l.inventoryFoundryNetwork(ctx, plan, &state); err != nil {
			return fmt.Errorf("owned network inventory is unverified; dependencies retained: %w", err)
		}
	}
	if err := l.cleanupFoundry(ctx, plan, &state, purge, progress); err != nil {
		return fmt.Errorf("Foundry teardown is not confirmed; dependencies retained: %w", err)
	}
	if err := l.cleanupFoundryNetwork(ctx, plan, &state, progress); err != nil {
		return fmt.Errorf("owned network teardown is not confirmed; dependencies retained: %w", err)
	}
	if err := l.verifyServiceFICsGone(ctx, state); err != nil {
		return fmt.Errorf("service identity teardown is incomplete; dependency grants retained: %w", err)
	}
	for _, key := range slices.Sorted(maps.Keys(state.Assignments)) {
		if state.Assignments[key].Deleted {
			continue
		}
		if err := l.cleanupAssignment(ctx, &state, key); err != nil {
			return fmt.Errorf("owned assignment cleanup stopped; retain the journal and resume azd down: %w", err)
		}
	}
	for _, step := range []string{"R02", "R03", "R05", "R04", "R06Subnet"} {
		if record, exists := state.Resources[step]; exists {
			if record.Deleted {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if progress != nil {
				progress(fmt.Sprintf("Deleting owned resource %s...", record.ID))
			}
			if err := l.cleanupResource(ctx, plan, &state, step, record); err != nil {
				return fmt.Errorf("%s cleanup stopped; retain the journal and resume azd down: %w", step, err)
			}
		}
	}
	state.CleanupComplete = true
	state.ResourceProgress = nil
	if err := l.journal.Save(ctx, state); err != nil {
		return fmt.Errorf("persist cleanup completion: %w", err)
	}
	if progress != nil {
		progress("Owned cleanup complete; resource groups, supplied resources and reused grants were retained")
	}
	return nil
}

func validateCleanupLedger(plan Plan, state PreparationState) error {
	if state.Owner == "" && (len(state.Resources) > 0 || len(state.Assignments) > 0 || state.Foundry != nil) {
		return fmt.Errorf("cleanup requires recorded environment ownership")
	}
	types := map[string]string{
		"R01": "Microsoft.Resources/resourceGroups", "R02": "Microsoft.Storage/storageAccounts",
		"R03": "Microsoft.ContainerRegistry/registries", "R04": "Microsoft.ManagedIdentity/userAssignedIdentities",
		"R05": "Microsoft.ManagedIdentity/userAssignedIdentities", "R06Subnet": "Microsoft.Network/virtualNetworks/subnets",
	}
	for step, record := range state.Resources {
		kind, known := types[step]
		if !known {
			return fmt.Errorf("unknown owned resource step; refusing destructive interpretation of the journal")
		}
		id, err := ParseID(record.ID, kind)
		if err != nil {
			return fmt.Errorf("invalid owned resource in cleanup journal: %w", err)
		}
		if step != "R06Subnet" && (!strings.EqualFold(id.SubscriptionID, plan.SubscriptionID) ||
			!strings.EqualFold(id.ResourceGroupName, plan.ResourceGroup)) {
			return fmt.Errorf("owned resource scope differs from the recorded environment")
		}
		if step == "R06Subnet" && (state.Network == nil || !strings.EqualFold(state.Network.SubnetID, record.ID)) {
			return fmt.Errorf("owned subnet has no matching durable topology")
		}
	}
	for _, record := range state.Assignments {
		id, err := ParseID(record.ID, "Microsoft.Authorization/roleAssignments")
		if err != nil || !strings.EqualFold(id.Parent.String(), record.Scope) ||
			record.PrincipalID == "" || record.RoleID == "" {
			return fmt.Errorf("invalid exact assignment ownership in cleanup journal")
		}
	}
	if state.Foundry != nil {
		return validateFoundrySpec(plan, state.Foundry.Spec)
	}
	return nil
}

func (l *ownedLifecycle) cleanupAssignment(ctx context.Context, state *PreparationState, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	record := state.Assignments[key]
	id, err := ParseID(record.ID, "Microsoft.Authorization/roleAssignments")
	if err != nil {
		return err
	}
	client, err := armauthorization.NewRoleAssignmentsClient(id.SubscriptionID, l.credential, l.options)
	if err != nil {
		return err
	}
	observed, err := client.GetByID(ctx, record.ID, nil)
	if !responseNotFound(err) {
		if err != nil {
			return err
		}
		if !strings.EqualFold(authString(observed.ID), record.ID) ||
			!compatibleGrant(observed.RoleAssignment, grantRequest{
				scope: record.Scope, principalID: record.PrincipalID, roleID: record.RoleID,
			}) || authString(observed.Properties.Description) != state.Owner {
			return fmt.Errorf("owned assignment %s ownership/configuration changed", record.ID)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := client.DeleteByID(ctx, record.ID, nil); err != nil && !responseNotFound(err) {
			return err
		}
		if _, err := client.GetByID(ctx, record.ID, nil); !responseNotFound(err) {
			if err != nil {
				return fmt.Errorf("verify assignment deletion: %w", err)
			}
			return fmt.Errorf("assignment deletion is not yet confirmed; dependencies retained")
		}
	}
	record.Deleted = true
	state.Assignments[key] = record
	return l.journal.Save(ctx, *state)
}

func (l *ownedLifecycle) cleanupResource(
	ctx context.Context, plan Plan, state *PreparationState, step string, record OwnedResource,
) error {
	if step == "R06Subnet" {
		return l.cleanupSubnet(ctx, state, record)
	}
	if step == "R03" {
		return l.cleanupRegistry(ctx, state, record)
	}
	versions := map[string]string{"R02": "2024-01-01", "R04": "2024-11-30", "R05": "2024-11-30"}
	version := versions[step]
	if version == "" {
		return fmt.Errorf("resource type has no supported scoped delete contract")
	}
	client, err := armresources.NewClient(plan.SubscriptionID, l.credential, l.options)
	if err != nil {
		return err
	}
	observed, err := client.GetByID(ctx, record.ID, version, nil)
	if !responseNotFound(err) {
		if err != nil {
			return err
		}
		if err := confirmOwned(*state, step, authString(observed.ID), observed.Tags); err != nil {
			return err
		}
		if step == "R04" || step == "R05" {
			id, err := ParseID(record.ID, "Microsoft.ManagedIdentity/userAssignedIdentities")
			if err != nil {
				return err
			}
			identities, err := armmsi.NewUserAssignedIdentitiesClient(id.SubscriptionID, l.credential, l.options)
			if err != nil {
				return err
			}
			identity, err := identities.Get(ctx, id.ResourceGroupName, id.Name, nil)
			if err != nil {
				return err
			}
			if err := verifyOwnedIdentity(*state, step, identity.Identity); err != nil {
				return err
			}
			fics, err := armmsi.NewFederatedIdentityCredentialsClient(id.SubscriptionID, l.credential, l.options)
			if err != nil {
				return err
			}
			pager := fics.NewListPager(id.ResourceGroupName, id.Name, nil)
			for pager.More() {
				page, err := pager.NextPage(ctx)
				if err != nil {
					return fmt.Errorf("verify service FIC teardown before identity deletion: %w", err)
				}
				if len(page.Value) > 0 {
					return fmt.Errorf("identity still has federated credentials; wait for service teardown; azd will not delete FICs")
				}
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		poller, err := client.BeginDeleteByID(ctx, record.ID, version, nil)
		if err != nil && !responseNotFound(err) {
			return err
		}
		if err == nil {
			if _, err := poller.PollUntilDone(ctx, nil); err != nil {
				return err
			}
		}
		if _, err := client.GetByID(ctx, record.ID, version, nil); !responseNotFound(err) {
			if err != nil {
				return fmt.Errorf("verify resource deletion: %w", err)
			}
			return fmt.Errorf("owned resource deletion is not confirmed")
		}
	}
	record.Deleted = true
	state.Resources[step] = record
	return l.journal.Save(ctx, *state)
}

func (l *ownedLifecycle) cleanupRegistry(ctx context.Context, state *PreparationState, record OwnedResource) error {
	id, err := ParseID(record.ID, "Microsoft.ContainerRegistry/registries")
	if err != nil {
		return err
	}
	client, err := armcontainerregistry.NewRegistriesClient(id.SubscriptionID, l.credential, l.options)
	if err != nil {
		return err
	}
	observed, err := client.Get(ctx, id.ResourceGroupName, id.Name, nil)
	if !responseNotFound(err) {
		if err != nil {
			return err
		}
		if err := confirmOwned(*state, "R03", authString(observed.ID), observed.Tags); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		poller, err := client.BeginDelete(ctx, id.ResourceGroupName, id.Name, nil)
		if err != nil && !responseNotFound(err) {
			return err
		}
		if err == nil {
			if _, err := poller.PollUntilDone(ctx, nil); err != nil {
				return err
			}
		}
		if _, err := client.Get(ctx, id.ResourceGroupName, id.Name, nil); !responseNotFound(err) {
			if err != nil {
				return fmt.Errorf("verify registry deletion: %w", err)
			}
			return fmt.Errorf("owned registry deletion is not confirmed")
		}
	}
	record.Deleted = true
	state.Resources["R03"] = record
	return l.journal.Save(ctx, *state)
}

func verifyOwnedIdentity(state PreparationState, step string, identity armmsi.Identity) error {
	if err := confirmOwned(state, step, authString(identity.ID), identity.Tags); err != nil {
		return err
	}
	record := state.Resources[step].Identity
	if record.PrincipalID != "" && (identity.Properties == nil ||
		!strings.EqualFold(authString(identity.Properties.PrincipalID), record.PrincipalID) ||
		!strings.EqualFold(authString(identity.Properties.ClientID), record.ClientID) ||
		!strings.EqualFold(authString(identity.Properties.TenantID), record.TenantID)) {
		return fmt.Errorf("owned identity principal/client/tenant changed; refusing deletion")
	}
	return nil
}

func (l *ownedLifecycle) cleanupSubnet(ctx context.Context, state *PreparationState, record OwnedResource) error {
	id, err := ParseID(record.ID, "Microsoft.Network/virtualNetworks/subnets")
	if err != nil {
		return err
	}
	client, err := armnetwork.NewSubnetsClient(id.SubscriptionID, l.credential, l.options)
	if err != nil {
		return err
	}
	observed, err := client.Get(ctx, id.ResourceGroupName, id.Parent.Name, id.Name, nil)
	if !responseNotFound(err) {
		if err != nil {
			return err
		}
		p := observed.Properties
		if !strings.EqualFold(authString(observed.ID), record.ID) || p == nil || state.Network == nil ||
			authString(p.AddressPrefix) != state.Network.AddressPrefix || len(p.AddressPrefixes) != 0 ||
			p.ProvisioningState == nil || string(*p.ProvisioningState) != "Succeeded" ||
			p.NetworkSecurityGroup != nil || p.RouteTable != nil || p.NatGateway != nil ||
			len(p.IpamPoolPrefixAllocations) != 0 || p.SharingScope != nil ||
			len(p.IPConfigurations)+len(p.IPConfigurationProfiles)+len(p.IPAllocations)+
				len(p.PrivateEndpoints)+len(p.ServiceAssociationLinks)+len(p.ResourceNavigationLinks)+
				len(p.ApplicationGatewayIPConfigurations)+len(p.ServiceEndpoints)+len(p.ServiceEndpointPolicies) != 0 ||
			len(p.Delegations) != 1 || p.Delegations[0] == nil || p.Delegations[0].Properties == nil ||
			authString(p.Delegations[0].Properties.ServiceName) != "Microsoft.App/environments" {
			return fmt.Errorf("journaled subnet topology changed or remains in use; refusing deletion")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		poller, err := client.BeginDelete(ctx, id.ResourceGroupName, id.Parent.Name, id.Name, nil)
		if err != nil && !responseNotFound(err) {
			return err
		}
		if err == nil {
			if _, err := poller.PollUntilDone(ctx, nil); err != nil {
				return err
			}
		}
		if _, err := client.Get(ctx, id.ResourceGroupName, id.Parent.Name, id.Name, nil); !responseNotFound(err) {
			if err != nil {
				return err
			}
			return fmt.Errorf("owned subnet deletion is not confirmed")
		}
	}
	record.Deleted = true
	state.Resources["R06Subnet"] = record
	return l.journal.Save(ctx, *state)
}
