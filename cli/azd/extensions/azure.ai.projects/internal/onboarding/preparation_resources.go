// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
)

func (p *ownedPreparer) Prepare(ctx context.Context, plan Plan, facts Facts) (Prepared, error) {
	var result Prepared
	// Only the resolved same-VNet delegated subnet is mutable; broader networking remains administrator-owned.
	if plan.NetworkChanges {
		return result, fmt.Errorf("R06: selected companion network mutation/recovery is unsupported")
	}
	if plan.AgentSubnet.ID == "" {
		return result, fmt.Errorf("R06: an existing or read-only-resolved delegated subnet is required")
	}
	if p.network == nil {
		return result, fmt.Errorf("R06: post-creation network verifier is required")
	}
	for _, op := range plan.Operations {
		if strings.HasPrefix(strings.ToLower(op.Action), "microsoft.network/") &&
			!strings.HasSuffix(strings.ToLower(op.Action), "/read") &&
			!(op.CheckID == "P03" && strings.EqualFold(op.Action, "Microsoft.Network/virtualNetworks/write")) &&
			!selectedSubnetOperation(plan, op) {
			return result, fmt.Errorf(
				"R06: selected network mutations are unsupported; administrator preparation is required")
		}
	}
	if facts.TenantID == "" || facts.KubeletObjectID == "" || plan.Location == "" ||
		plan.SubscriptionID == "" || plan.ResourceGroup == "" {
		return result, fmt.Errorf("preparation requires verified tenant, kubelet object ID and target location/scope")
	}
	if plan.Hosting.Supplied || plan.Workload.Supplied ||
		strings.EqualFold(plan.Hosting.ID, plan.Workload.ID) {
		return result, fmt.Errorf("R04/R05: two distinct internally planned identities are required")
	}
	for i, resource := range []Resource{plan.Hosting, plan.Workload, plan.Storage, plan.Registry} {
		id, err := arm.ParseResourceID(resource.ID)
		if err != nil || id.SubscriptionID == "" || id.ResourceGroupName == "" || id.Name == "" {
			return result, fmt.Errorf("preparation resource has an invalid ARM ID")
		}
		types := []string{
			"Microsoft.ManagedIdentity/userAssignedIdentities", "Microsoft.ManagedIdentity/userAssignedIdentities",
			"Microsoft.Storage/storageAccounts", "Microsoft.ContainerRegistry/registries",
		}
		if !strings.EqualFold(id.ResourceType.String(), types[i]) {
			return result, fmt.Errorf("preparation resource ID has an unexpected ARM type")
		}
		if !resource.Supplied &&
			(!strings.EqualFold(id.SubscriptionID, plan.SubscriptionID) ||
				!strings.EqualFold(id.ResourceGroupName, plan.ResourceGroup)) {
			return result, fmt.Errorf("owned preparation resources must use the planned subscription and resource group")
		}
	}
	// Resolve all grant contracts before creating dependencies; never fall back to a broader role.
	grants := grantRequests(plan, facts, Identity{}, Identity{})
	for i := range grants {
		id, err := resolveGrantRole(ctx, p.credential, p.options, plan.SubscriptionID, grants[i])
		if err != nil {
			return result, fmt.Errorf("%s: resolve prerequisite role: %w", grants[i].checkID, err)
		}
		grants[i].roleID = id
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	state, err := p.beginState(ctx, plan)
	if err != nil {
		return result, err
	}
	result.ResourceGroupTags, err = p.prepareGroup(ctx, plan, &state)
	if err != nil {
		return result, err
	}
	if err := p.prepareNetwork(ctx, plan, &state); err != nil {
		return result, err
	}
	result.Hosting, err = p.prepareIdentity(ctx, plan, &state, "R04", plan.Hosting, facts.TenantID)
	if err != nil {
		return result, err
	}
	result.Workload, err = p.prepareIdentity(ctx, plan, &state, "R05", plan.Workload, facts.TenantID)
	if err != nil {
		return result, err
	}
	if strings.EqualFold(result.Hosting.ID, result.Workload.ID) ||
		strings.EqualFold(result.Hosting.PrincipalID, result.Workload.PrincipalID) ||
		strings.EqualFold(result.Hosting.ClientID, result.Workload.ClientID) {
		return result, fmt.Errorf("R04/R05: hosting and workload identities are not independent")
	}
	if err := p.prepareStorage(ctx, plan, &state); err != nil {
		return result, err
	}
	result.RegistryEndpoint, result.RegistryABAC, err = p.prepareRegistry(ctx, plan, &state, facts.RegistryABAC)
	if err != nil {
		return result, err
	}
	for i := range grants {
		switch grants[i].checkID {
		case "G01", "G02":
			grants[i].principalID = result.Hosting.PrincipalID
		case "G03":
			grants[i].principalID = result.Workload.PrincipalID
		}
		if err := p.prepareGrant(ctx, &state, grants[i]); err != nil {
			return result, err
		}
	}
	result.StorageID, result.RegistryID = plan.Storage.ID, plan.Registry.ID
	result.AgentSubnetID = plan.AgentSubnet.ID
	report, err := p.network.VerifyPreparedNetwork(ctx, plan, result)
	if err != nil {
		return result, fmt.Errorf("R06: verify prepared network: %w", err)
	}
	if len(report) == 0 {
		return result, fmt.Errorf("R06: prepared network verifier returned no evidence; retain the ownership journal")
	}
	if err := report.Err(); err != nil {
		return result, fmt.Errorf("R06: prepared path is not verified; retain ownership journal for recovery: %w", err)
	}
	return result, nil
}

func responseNotFound(err error) bool {
	response, ok := errors.AsType[*azcore.ResponseError](err)
	return ok && response.StatusCode == 404
}

func (p *ownedPreparer) prepareGroup(
	ctx context.Context, plan Plan, state *PreparationState,
) (map[string]*string, error) {
	client, err := armresources.NewResourceGroupsClient(plan.SubscriptionID, p.credential, p.options)
	if err != nil {
		return nil, err
	}
	id := "/subscriptions/" + plan.SubscriptionID + "/resourceGroups/" + plan.ResourceGroup
	found, err := client.Get(ctx, plan.ResourceGroup, nil)
	if err == nil {
		if _, tracked := state.Resources["R01"]; tracked {
			if err := confirmOwned(*state, "R01", authString(found.ID), found.Tags); err != nil {
				return nil, err
			}
		}
		tags := map[string]*string{}
		maps.Copy(tags, found.Tags)
		return tags, nil
	}
	if !responseNotFound(err) {
		return nil, fmt.Errorf("R01: read target resource group: %w", err)
	}
	if err := p.resourceIntent(ctx, state, "R01", id); err != nil {
		return nil, err
	}
	_, err = client.CreateOrUpdate(ctx, plan.ResourceGroup, armresources.ResourceGroup{
		Location: new(plan.Location), Tags: ownerTags(plan.Owner),
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("R01: create resource group: %w", err)
	}
	readback, err := client.Get(ctx, plan.ResourceGroup, nil)
	if err != nil {
		return nil, fmt.Errorf("R01: read created resource group: %w", err)
	}
	if err := confirmOwned(*state, "R01", authString(readback.ID), readback.Tags); err != nil {
		return nil, err
	}
	return maps.Clone(readback.Tags), p.resourceComplete(ctx, state, "R01", id)
}

func (p *ownedPreparer) prepareIdentity(
	ctx context.Context, plan Plan, state *PreparationState, step string, resource Resource, tenant string,
) (Identity, error) {
	var result Identity
	id, err := arm.ParseResourceID(resource.ID)
	if err != nil {
		return result, err
	}
	client, err := armmsi.NewUserAssignedIdentitiesClient(id.SubscriptionID, p.credential, p.options)
	if err != nil {
		return result, err
	}
	response, err := client.Get(ctx, id.ResourceGroupName, id.Name, nil)
	if responseNotFound(err) {
		if err := p.resourceIntent(ctx, state, step, resource.ID); err != nil {
			return result, err
		}
		_, err = client.CreateOrUpdate(ctx, id.ResourceGroupName, id.Name, armmsi.Identity{
			Location: new(plan.Location), Tags: ownerTags(plan.Owner),
		}, nil)
		if err != nil {
			return result, fmt.Errorf("%s: create identity: %w", step, err)
		}
		response, err = client.Get(ctx, id.ResourceGroupName, id.Name, nil)
	}
	if err != nil {
		return result, fmt.Errorf("%s: read identity: %w", step, err)
	}
	if err := confirmOwned(*state, step, authString(response.ID), response.Tags); err != nil {
		return result, err
	}
	if response.Properties == nil {
		return result, fmt.Errorf("%s: identity properties are unavailable", step)
	}
	properties := response.Properties
	result = Identity{
		ID: authString(response.ID), PrincipalID: authString(properties.PrincipalID),
		ClientID: authString(properties.ClientID), TenantID: authString(properties.TenantID),
	}
	if result.PrincipalID == "" || result.ClientID == "" || !strings.EqualFold(result.TenantID, tenant) {
		return result, fmt.Errorf("%s: identity object/client/tenant evidence is incomplete or mismatched", step)
	}
	record := state.Resources[step]
	if record.Identity.PrincipalID != "" &&
		(!strings.EqualFold(record.Identity.PrincipalID, result.PrincipalID) ||
			!strings.EqualFold(record.Identity.ClientID, result.ClientID) ||
			!strings.EqualFold(record.Identity.TenantID, result.TenantID)) {
		return result, fmt.Errorf("%s: recorded identity object/client/tenant IDs changed; refusing replacement", step)
	}
	record.Identity = result
	state.Resources[step] = record
	return result, p.resourceComplete(ctx, state, step, resource.ID)
}

func safeStorageParameters(plan Plan) armstorage.AccountCreateParameters {
	return armstorage.AccountCreateParameters{
		Location: new(plan.Location), Kind: new(armstorage.KindStorageV2),
		SKU: &armstorage.SKU{Name: new(armstorage.SKUNameStandardLRS)}, Tags: ownerTags(plan.Owner),
		Properties: &armstorage.AccountPropertiesCreateParameters{
			AccessTier: new(armstorage.AccessTierHot), IsHnsEnabled: new(false),
			EnableHTTPSTrafficOnly: new(true), MinimumTLSVersion: new(armstorage.MinimumTLSVersionTLS12),
			AllowSharedKeyAccess: new(false), AllowBlobPublicAccess: new(false),
			PublicNetworkAccess: new(armstorage.PublicNetworkAccessEnabled),
			NetworkRuleSet:      &armstorage.NetworkRuleSet{DefaultAction: new(armstorage.DefaultActionAllow)},
		},
	}
}

func (p *ownedPreparer) prepareStorage(ctx context.Context, plan Plan, state *PreparationState) error {
	id, _ := arm.ParseResourceID(plan.Storage.ID)
	client, err := armstorage.NewAccountsClient(id.SubscriptionID, p.credential, p.options)
	if err != nil {
		return err
	}
	response, err := client.GetProperties(ctx, id.ResourceGroupName, id.Name, nil)
	if !plan.Storage.Supplied && responseNotFound(err) {
		if err := p.resourceIntent(ctx, state, "R02", plan.Storage.ID); err != nil {
			return err
		}
		poller, createErr := client.BeginCreate(ctx, id.ResourceGroupName, id.Name, safeStorageParameters(plan), nil)
		if createErr != nil {
			return fmt.Errorf("R02: create Storage: %w", createErr)
		}
		if _, err := poller.PollUntilDone(ctx, nil); err != nil {
			return fmt.Errorf("R02: wait for Storage creation; retry using the ownership journal: %w", err)
		}
		response, err = client.GetProperties(ctx, id.ResourceGroupName, id.Name, nil)
	}
	if err != nil {
		return fmt.Errorf("R02: read Storage: %w", err)
	}
	if !plan.Storage.Supplied {
		if err := confirmOwned(*state, "R02", authString(response.ID), response.Tags); err != nil {
			return err
		}
	}
	if !strings.EqualFold(authString(response.ID), plan.Storage.ID) {
		return fmt.Errorf("R02: Storage readback ID does not match the plan")
	}
	if err := storageResourceChecks(response.Account).Err(); err != nil {
		return fmt.Errorf("R02: Storage security readback: %w", err)
	}
	if response.Properties == nil || response.Properties.ProvisioningState == nil ||
		*response.Properties.ProvisioningState != armstorage.ProvisioningStateSucceeded {
		return fmt.Errorf("R02: Storage provisioning readback is incomplete or noncompliant")
	}
	if !plan.Storage.Supplied {
		return p.resourceComplete(ctx, state, "R02", plan.Storage.ID)
	}
	return nil
}

func (p *ownedPreparer) prepareRegistry(
	ctx context.Context, plan Plan, state *PreparationState, expectedABAC bool,
) (string, bool, error) {
	client, err := newRegistryARMClient(p.credential, p.options)
	if err != nil {
		return "", false, err
	}
	response, err := client.get(ctx, plan.Registry.ID)
	if !plan.Registry.Supplied && responseNotFound(err) {
		if err := p.resourceIntent(ctx, state, "R03", plan.Registry.ID); err != nil {
			return "", false, err
		}
		mode := "LegacyRegistryPermissions"
		if expectedABAC {
			mode = "AbacRepositoryPermissions"
		}
		createErr := client.create(ctx, plan.Registry.ID, registryCreateRequest{
			Location: plan.Location, Tags: ownerTags(plan.Owner),
			SKU: &armcontainerregistry.SKU{Name: new(armcontainerregistry.SKUNamePremium)},
			Properties: &registryCreateProperties{
				RoleAssignmentMode: mode, PublicNetworkAccess: "Enabled",
			},
		})
		if createErr != nil {
			return "", false, fmt.Errorf("R03: create registry: %w", createErr)
		}
		response, err = client.get(ctx, plan.Registry.ID)
	}
	if err != nil {
		return "", false, fmt.Errorf("R03: read registry: %w", err)
	}
	if !plan.Registry.Supplied {
		if err := confirmOwned(*state, "R03", authString(response.ID), response.Tags); err != nil {
			return "", false, err
		}
	}
	if !strings.EqualFold(authString(response.ID), plan.Registry.ID) || response.Properties == nil {
		return "", false, fmt.Errorf("R03: registry readback ID or properties do not match the plan")
	}
	props := response.Properties
	if props.ProvisioningState == nil || *props.ProvisioningState != armcontainerregistry.ProvisioningStateSucceeded ||
		response.AuthorizationMode == nil || props.LoginServer == nil || *props.LoginServer == "" {
		return "", false, fmt.Errorf("R03: registry readiness, endpoint or authorization mode is unverified")
	}
	mode := *response.AuthorizationMode
	if mode != "LegacyRegistryPermissions" && mode != "AbacRepositoryPermissions" {
		return "", false, fmt.Errorf("R03: registry authorization mode is unsupported")
	}
	abac := mode == "AbacRepositoryPermissions"
	if abac != expectedABAC {
		return "", false, fmt.Errorf("R03: registry authorization mode changed since preflight; rerun preflight")
	}
	if strings.ContainsAny(*props.LoginServer, "/:@?#") {
		return "", false, fmt.Errorf("R03: registry login endpoint is not a supported hostname")
	}
	if !plan.Registry.Supplied {
		if err := p.resourceComplete(ctx, state, "R03", plan.Registry.ID); err != nil {
			return "", false, err
		}
	}
	return *props.LoginServer, abac, nil
}
