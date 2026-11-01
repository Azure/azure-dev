// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/onboarding"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/google/uuid"
)

func (p *FoundryProvisioningProvider) prepareBYO(
	ctx context.Context, source *templateSource, progress grpcbroker.ProgressFunc,
) error {
	configured := p.synthResult != nil && p.synthResult.Hosting != nil
	value, err := byoHostingParameter(source)
	if err != nil {
		return err
	}
	var hosting struct {
		Enabled                             bool   `json:"enabled"`
		HostingType                         string `json:"hostingType"`
		Name                                string `json:"name"`
		ClusterResourceID                   string `json:"clusterResourceId"`
		StorageAccountResourceID            string `json:"storageAccountResourceId"`
		HostingManagementIdentityResourceID string `json:"hostingManagementIdentityResourceId"`
		WorkloadIdentityResourceID          string `json:"workloadIdentityResourceId"`
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode agentHosting deployment parameters: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&hosting); err != nil {
		return exterrors.Validation(exterrors.CodeInvalidParameter,
			fmt.Sprintf("invalid agentHosting deployment parameters: %s", err),
			"supply agentHosting as a literal object matching azure.yaml, not an ARM expression")
	}
	if !configured {
		if !hosting.Enabled {
			if hosting.StorageAccountResourceID != "" || hosting.HostingManagementIdentityResourceID != "" ||
				hosting.WorkloadIdentityResourceID != "" {
				return exterrors.Validation(exterrors.CodeConflictingArguments,
					"supporting-resource selections cannot take effect without enabled agentHosting in azure.yaml",
					"declare agentHosting and its resource selections on the azure.ai.project service, "+
						"or remove the selections")
			}
			return nil
		}
		return exterrors.Validation(exterrors.CodeConflictingArguments,
			"effective agentHosting deployment parameters enable BYO AKS without agentHosting in azure.yaml",
			"set agentHosting.clusterResourceId on the azure.ai.project service in azure.yaml, "+
				"or disable agentHosting in the parameters file and template defaults")
	}
	includeAcr, ok := byoParameterValue(source, "includeAcr").(bool)
	if !ok {
		return exterrors.Validation(exterrors.CodeInvalidParameter,
			"includeAcr must be a boolean", "set includeAcr to true or false")
	}
	want := p.synthResult.Hosting
	if !hosting.Enabled || hosting.HostingType != "ManagedCluster" || hosting.Name != "default" ||
		!strings.EqualFold(hosting.ClusterResourceID, want.ClusterResourceID) ||
		!strings.EqualFold(hosting.StorageAccountResourceID, want.StorageAccountResourceID) ||
		!strings.EqualFold(hosting.HostingManagementIdentityResourceID, want.HostingManagementIdentityResourceID) ||
		!strings.EqualFold(hosting.WorkloadIdentityResourceID, want.WorkloadIdentityResourceID) {
		return fmt.Errorf("agentHosting deployment parameters conflict with azure.yaml; " +
			"clusterResourceId and every supporting-resource selection must match")
	}
	for _, selection := range []struct {
		id, kind string
	}{
		{hosting.ClusterResourceID, "Microsoft.ContainerService/managedClusters"},
		{hosting.StorageAccountResourceID, "Microsoft.Storage/storageAccounts"},
		{hosting.HostingManagementIdentityResourceID, "Microsoft.ManagedIdentity/userAssignedIdentities"},
		{hosting.WorkloadIdentityResourceID, "Microsoft.ManagedIdentity/userAssignedIdentities"},
	} {
		if selection.id != "" {
			if _, err := onboarding.ParseID(selection.id, selection.kind); err != nil {
				return err
			}
		}
	}
	if hosting.HostingManagementIdentityResourceID != "" &&
		strings.EqualFold(hosting.HostingManagementIdentityResourceID, hosting.WorkloadIdentityResourceID) {
		return fmt.Errorf("hostingManagementIdentityResourceId and workloadIdentityResourceId must be distinct")
	}
	hasSupplied := hosting.StorageAccountResourceID != "" ||
		hosting.HostingManagementIdentityResourceID != "" || hosting.WorkloadIdentityResourceID != ""
	if hasSupplied {
		if err := validateBYOSupportingTemplate(source.armTemplate); err != nil {
			return err
		}
		for _, name := range []string{"location", "resourceTokenSalt"} {
			value, ok := byoParameterValue(source, name).(string)
			if !ok || name == "location" && strings.TrimSpace(value) == "" {
				return fmt.Errorf("effective %s must be a literal string, nonempty except resourceTokenSalt", name)
			}
		}
	}
	resourceGroup, ok := byoParameterValue(source, "resourceGroupName").(string)
	if !ok || strings.TrimSpace(resourceGroup) == "" {
		return fmt.Errorf("effective resourceGroupName must be a nonempty string")
	}
	if hasSupplied && (p.subID == "" || strings.ContainsAny(p.subID+resourceGroup, "/\\?#%@:\x00 \t\r\n")) {
		return fmt.Errorf("I03: target subscription and resource group must identify a valid ARM scope")
	}
	suppliedBindings := map[string]string{}
	for name, parameter := range source.parameters {
		key := strings.ToLower(name)
		switch key {
		case "hostingprincipalid":
			if hosting.HostingManagementIdentityResourceID == "" {
				return fmt.Errorf("hostingPrincipalId requires agentHosting.hostingManagementIdentityResourceId")
			}
		case "workloadprincipalid":
			if hosting.WorkloadIdentityResourceID == "" {
				return fmt.Errorf("workloadPrincipalId requires agentHosting.workloadIdentityResourceId")
			}
		case "kubeletobjectid", "federatedidentityroledefinitionid":
		default:
			continue
		}
		if !includeAcr && strings.EqualFold(name, "kubeletObjectId") {
			continue
		}
		if _, duplicate := suppliedBindings[key]; duplicate {
			return exterrors.Validation(exterrors.CodeConflictingArguments,
				fmt.Sprintf("multiple %s deployment parameters differ only by case", name),
				"supply the binding only once using the template's parameter name")
		}
		envelope, _ := parameter.(map[string]any)
		value, ok := envelope["value"].(string)
		if !ok || strings.TrimSpace(value) == "" {
			return exterrors.Validation(exterrors.CodeInvalidParameter,
				fmt.Sprintf("%s must be a nonempty string matching the provider-resolved BYO AKS binding", name),
				fmt.Sprintf("remove %s from the parameters file so microsoft.foundry can resolve it automatically", name))
		}
		suppliedBindings[key] = value
	}
	if err := p.ensureCredential(ctx); err != nil {
		return err
	}
	plan, err := p.byoResourcePlan(ctx, source, hasSupplied)
	if err != nil {
		return err
	}
	pipeline := onboarding.Pipeline{
		Resources: onboarding.NewResourceChecker(p.credential, p.armClientOptions),
		Access:    onboarding.NewAuthorizer(p.credential, p.armClientOptions),
	}
	facts, report, preflightErr := pipeline.Preflight(ctx, plan)
	if err := writeBYOPreflight(report, preflightErr, progress); err != nil {
		return err
	}
	bindRegistry := includeAcr || plan.Registry.ID != "" || plan.Registry.Missing != ""
	kubeletObjectID, federationRoleID, err := p.resolveBYOHostBindings(ctx, plan, facts, bindRegistry)
	if err != nil {
		return err
	}
	resolvedBindings := map[string]string{
		"federatedIdentityRoleDefinitionId": federationRoleID,
	}
	if bindRegistry {
		resolvedBindings["kubeletObjectId"] = kubeletObjectID
	}
	if hosting.HostingManagementIdentityResourceID != "" {
		resolvedBindings["hostingPrincipalId"] = facts.HostingIdentity.PrincipalID
	}
	if hosting.WorkloadIdentityResourceID != "" {
		resolvedBindings["workloadPrincipalId"] = facts.WorkloadIdentity.PrincipalID
	}
	for name, expected := range resolvedBindings {
		if value, supplied := suppliedBindings[strings.ToLower(name)]; supplied && !strings.EqualFold(value, expected) {
			return exterrors.Validation(exterrors.CodeConflictingArguments,
				fmt.Sprintf("%s conflicts with the provider-resolved agentHosting binding", name),
				fmt.Sprintf("remove %s from the parameters file so microsoft.foundry can resolve it automatically", name))
		}
	}
	bindings := map[string]any{"agentHosting": map[string]any{"value": hosting}}
	for name, value := range resolvedBindings {
		bindings[name] = map[string]any{"value": value}
	}
	hostBindings := parametersDeclaredByTemplate(bindings, source.armTemplate)
	for name := range source.parameters {
		for binding := range hostBindings {
			if strings.EqualFold(name, binding) {
				delete(source.parameters, name)
			}
		}
	}
	source.parameters = mergeParameters(source.parameters, hostBindings)
	return nil
}

func (p *FoundryProvisioningProvider) byoResourcePlan(
	ctx context.Context, source *templateSource, hasSupplied bool,
) (onboarding.Plan, error) {
	if !hasSupplied {
		return p.identifyBYOTemplatePlan(ctx, source)
	}
	selection := p.synthResult.Hosting
	group := byoParameterValue(source, "resourceGroupName").(string)
	_, groupExists, err := p.lookupResourceGroupState(ctx, group)
	if err != nil {
		return onboarding.Plan{}, err
	}
	defaults := byoSupportingNames{}
	includeAcr, _ := byoParameterValue(source, "includeAcr").(bool)
	if selection.StorageAccountResourceID == "" || selection.HostingManagementIdentityResourceID == "" ||
		selection.WorkloadIdentityResourceID == "" || includeAcr {
		defaults, err = p.resolveBYOSupportingNames(ctx, source, groupExists)
		if err != nil {
			return onboarding.Plan{}, err
		}
	}
	registryID := ""
	if includeAcr {
		registryID = defaults.RegistryID
	}
	plan, err := onboarding.BuildPlan(onboarding.Input{
		SubscriptionID: p.subID, ResourceGroup: group, GroupExists: groupExists,
		ClusterID: selection.ClusterResourceID, StorageID: selection.StorageAccountResourceID,
		HostingIdentityID:  selection.HostingManagementIdentityResourceID,
		WorkloadIdentityID: selection.WorkloadIdentityResourceID,
		DefaultStorageID:   defaults.StorageID, DefaultHostingID: defaults.HostingID, DefaultWorkloadID: defaults.WorkloadID,
		DefaultRegistryID: registryID,
	})
	if err != nil {
		return onboarding.Plan{}, err
	}
	selected, err := byoDeploymentSelection(source)
	if err != nil {
		return onboarding.Plan{}, err
	}
	plan, err = onboarding.WithDeploymentOperations(plan, selected)
	if err != nil {
		return onboarding.Plan{}, err
	}
	return plan, err
}

func byoDeploymentSelection(source *templateSource) (onboarding.DeploymentSelection, error) {
	selection := onboarding.DeploymentSelection{}
	var network onboarding.LegacyNetwork
	for key, out := range map[string]*bool{
		"enableNetworkIsolation": &network.Enabled, "createAgentSubnet": &network.CreateAgentSubnet,
		"createPESubnet": &network.CreatePESubnet, "useManagedEgress": &network.ManagedEgress,
	} {
		if raw := byoParameterValue(source, key); raw != nil {
			value, ok := raw.(bool)
			if !ok {
				return selection, fmt.Errorf("I03: effective %s must be a boolean", key)
			}
			*out = value
		}
	}
	for key, out := range map[string]*string{
		"vnetId": &network.VNetID, "agentSubnetName": &network.AgentSubnetName, "peSubnetName": &network.PESubnetName,
		"peSubnetPrefix":       &network.PESubnetPrefix,
		"managedIsolationMode": &network.IsolationMode, "dnsZonesResourceGroup": &network.DNSResourceGroup,
		"dnsZonesSubscription": &network.DNSSubscription,
	} {
		if raw := byoParameterValue(source, key); raw != nil {
			value, ok := raw.(string)
			if !ok {
				return selection, fmt.Errorf("I03: effective %s must be a string", key)
			}
			*out = value
		}
	}
	if network.Enabled {
		selection.Network = &network
	}
	return selection, nil
}

func writeBYOPreflight(report onboarding.Report, preflightErr error, progress grpcbroker.ProgressFunc) error {
	var output strings.Builder
	if err := report.Write(&output); err != nil {
		return errors.Join(preflightErr, err)
	}
	if output.Len() > 0 {
		progress(output.String())
	}
	return preflightErr
}

func byoARMOptions(options *arm.ClientOptions) *arm.ClientOptions {
	var result arm.ClientOptions
	if options != nil {
		result = *options
	}
	result.DisableRPRegistration = true
	return &result
}

func parseBYOResourceID(value, kind string) (*arm.ResourceID, error) {
	if !strings.HasPrefix(strings.ToLower(value), "/subscriptions/") ||
		strings.ContainsAny(value, "?#%\\@:\x00 \t\r\n") || strings.Contains(value, "//") {
		return nil, fmt.Errorf("expected a complete %s resource ID without URL components", kind)
	}
	id, err := arm.ParseResourceID(value)
	if err != nil {
		return nil, fmt.Errorf("invalid %s resource ID: %w", kind, err)
	}
	if id.SubscriptionID == "" || id.ResourceGroupName == "" || id.Name == "" ||
		!strings.EqualFold(id.ResourceType.String(), kind) {
		return nil, fmt.Errorf("expected a complete %s resource ID", kind)
	}
	return id, nil
}

func (p *FoundryProvisioningProvider) resolveBYOHostBindings(
	ctx context.Context, plan onboarding.Plan, facts onboarding.Facts, includeAcr bool,
) (string, string, error) {
	options := byoARMOptions(p.armClientOptions)
	var kubeletObjectID string
	if includeAcr {
		if plan.Cluster.Missing != "" {
			return "", "", fmt.Errorf("cannot resolve deployment binding kubeletObjectId: actual clusterResourceId "+
				"is unknown (%s); this binding is required by the registry pull assignment, not only preflight",
				plan.Cluster.Missing)
		}
		kubeletObjectID = facts.KubeletObjectID
		if kubeletObjectID == "" {
			return "", "", fmt.Errorf("BYO AKS requires a kubelet principal object ID for registry access")
		}
		if _, err := uuid.Parse(kubeletObjectID); err != nil {
			return "", "", fmt.Errorf("AKS kubelet principal object ID must be a GUID: %w", err)
		}
	}
	const roleName = "Managed Identity Federated Identity Credential Contributor"
	workloadScope := plan.Workload.ID
	if workloadScope == "" {
		workloadScope = plan.Workload.Parent
	}
	workload, err := arm.ParseResourceID(workloadScope)
	if err != nil || workload.SubscriptionID == "" {
		return "", "", fmt.Errorf("cannot resolve deployment binding federatedIdentityRoleDefinitionId: actual "+
			"workload identity subscription is unknown (%s); "+
			"this binding is required by federationGrant, not only preflight",
			plan.Workload.Missing)
	}
	roleSubscription := workload.SubscriptionID
	roles, err := armauthorization.NewRoleDefinitionsClient(p.credential, options)
	if err != nil {
		return "", "", fmt.Errorf("create role definitions client: %w", err)
	}
	pager := roles.NewListPager("/subscriptions/"+roleSubscription,
		&armauthorization.RoleDefinitionsClientListOptions{Filter: new("roleName eq '" + roleName + "'")})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", "", fmt.Errorf("resolve built-in federation role: %w", err)
		}
		for _, role := range page.Value {
			if role == nil || role.Properties == nil || role.Properties.RoleName == nil ||
				*role.Properties.RoleName != roleName || role.Properties.RoleType == nil ||
				!strings.EqualFold(*role.Properties.RoleType, "BuiltInRole") {
				continue
			}
			if role.ID == nil {
				return "", "", fmt.Errorf("built-in federation role has an invalid ID")
			}
			roleID, err := arm.ParseResourceID(*role.ID)
			if err != nil || !strings.EqualFold(roleID.ResourceType.String(), "Microsoft.Authorization/roleDefinitions") {
				return "", "", fmt.Errorf("built-in federation role has an invalid ID")
			}
			if _, err := uuid.Parse(roleID.Name); err != nil {
				return "", "", fmt.Errorf("BYO AKS federation role requires a role definition GUID: %w", err)
			}
			return kubeletObjectID,
				"/subscriptions/" + roleSubscription +
					"/providers/Microsoft.Authorization/roleDefinitions/" + roleID.Name, nil
		}
	}
	return "", "", fmt.Errorf("required built-in role %q is unavailable", roleName)
}

func byoHostingParameter(source *templateSource) (any, error) {
	var value any
	definitions, _ := source.armTemplate["parameters"].(map[string]any)
	for name, definition := range definitions {
		if strings.EqualFold(name, "agentHosting") {
			if definition, ok := definition.(map[string]any); ok {
				value = definition["defaultValue"]
			}
		}
	}
	supplied := false
	for name, parameter := range source.parameters {
		if !strings.EqualFold(name, "agentHosting") {
			continue
		}
		if supplied {
			return nil, exterrors.Validation(exterrors.CodeConflictingArguments,
				"multiple agentHosting deployment parameters differ only by case",
				"supply agentHosting only once using the template's parameter name")
		}
		supplied = true
		envelope, ok := parameter.(map[string]any)
		literal, present := envelope["value"]
		if !ok || !present || len(envelope) != 1 {
			return nil, exterrors.Validation(exterrors.CodeInvalidParameter,
				"agentHosting deployment parameter must use a value envelope",
				"supply agentHosting as a literal object matching azure.yaml")
		}
		value = literal
	}
	return value, nil
}

func byoParameterValue(source *templateSource, key string) any {
	if envelope, ok := source.parameters[key].(map[string]any); ok {
		return envelope["value"]
	}
	if definitions, ok := source.armTemplate["parameters"].(map[string]any); ok {
		if definition, ok := definitions[key].(map[string]any); ok {
			return definition["defaultValue"]
		}
	}
	return nil
}
