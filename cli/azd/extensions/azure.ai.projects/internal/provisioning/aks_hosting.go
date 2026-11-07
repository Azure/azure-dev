// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore BYOAKS kubelet kubeletidentity
// cspell:ignore federatedidentityroledefinitionid hostingprincipalid kubeletobjectid workloadprincipalid

package provisioning

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/google/uuid"
)

type byoHostingParameters struct {
	Enabled                             bool            `json:"enabled"`
	ClusterResourceID                   json.RawMessage `json:"clusterResourceId"`
	HostingManagementIdentityResourceID json.RawMessage `json:"hostingManagementIdentityResourceId"`
	WorkloadIdentityResourceID          json.RawMessage `json:"workloadIdentityResourceId"`
}

func (p *FoundryProvisioningProvider) prepareBYO(ctx context.Context, source *templateSource) error {
	hosting, err := byoHostingParameter(source)
	if err != nil {
		return err
	}
	registryID, _ := parameterValue(source, "existingAcrResourceId").(string)
	if hosting.Enabled && (registryID != "" ||
		p.acrBinding != nil && p.acrBinding.Mode == "reuse-connect") {
		return synthesis.ErrACRReuseAgentHosting
	}
	if !hosting.Enabled {
		return nil
	}
	var clusterID string
	if err := json.Unmarshal(hosting.ClusterResourceID, &clusterID); err != nil {
		return exterrors.Validation(exterrors.CodeInvalidParameter,
			"agentHosting.clusterResourceId must be a resource ID string for AKS binding lookups",
			"supply the complete resource ID of the effective AKS cluster")
	}
	if _, err := parseBYOResourceID(clusterID, "Microsoft.ContainerService/managedClusters"); err != nil {
		return err
	}
	hostBindings := map[string]any{
		"federatedIdentityRoleDefinitionId": nil,
		"hostingPrincipalId":                nil,
		"workloadPrincipalId":               nil,
	}
	if includeAcr, _ := byoParameterValue(source, "includeAcr").(bool); includeAcr {
		hostBindings["kubeletObjectId"] = nil
	}
	hostBindings = parametersDeclaredByTemplate(hostBindings, source.armTemplate)
	for name := range hostBindings {
		for supplied := range source.parameters {
			if strings.EqualFold(name, supplied) {
				delete(hostBindings, name)
				break
			}
		}
	}
	_, resolveFederationRole := hostBindings["federatedIdentityRoleDefinitionId"]
	identities := []struct {
		field, binding, id, principal string
		value                         json.RawMessage
	}{
		{field: "hostingManagementIdentityResourceId", binding: "hostingPrincipalId",
			value: hosting.HostingManagementIdentityResourceID},
		{field: "workloadIdentityResourceId", binding: "workloadPrincipalId", value: hosting.WorkloadIdentityResourceID},
	}
	workloadID := ""
	for i := range identities {
		identity := &identities[i]
		if _, needed := hostBindings[identity.binding]; !needed &&
			!(resolveFederationRole && identity.binding == "workloadPrincipalId") {
			continue
		}
		if len(identity.value) > 0 {
			if err := json.Unmarshal(identity.value, &identity.id); err != nil {
				return exterrors.Validation(exterrors.CodeInvalidParameter,
					fmt.Sprintf("agentHosting.%s must be a resource ID string to resolve deployment bindings",
						identity.field),
					"supply a complete managed identity resource ID or provide the required deployment bindings explicitly")
			}
		}
		if identity.id == "" {
			delete(hostBindings, identity.binding)
			continue
		}
		if _, err := parseBYOResourceID(identity.id, "Microsoft.ManagedIdentity/userAssignedIdentities"); err != nil {
			return err
		}
		if identity.binding == "workloadPrincipalId" {
			workloadID = identity.id
		}
	}
	enabled, err := strconv.ParseBool(os.Getenv(synthesis.BYOAKSEnvVar))
	if err != nil || !enabled {
		return exterrors.Compatibility(
			exterrors.CodeFeatureNotEnabled,
			fmt.Sprintf("preview feature %q is disabled", synthesis.BYOAKSFeatureID),
			fmt.Sprintf("enable by setting %s=true in your shell or CI before running azd", synthesis.BYOAKSEnvVar),
		)
	}
	if err := p.ensureCredential(ctx); err != nil {
		return err
	}
	readCtx, cancelReads := context.WithCancelCause(ctx)
	defer cancelReads(nil)

	var reads sync.WaitGroup
	var kubeletObjectID, federationRoleID string
	reads.Go(func() {
		var err error
		kubeletObjectID, err = p.readBYOKubeletObjectID(readCtx, clusterID)
		if err != nil {
			cancelReads(err)
		}
	})
	if resolveFederationRole {
		reads.Go(func() {
			var err error
			federationRoleID, err = p.resolveBYOFederationRoleID(readCtx, workloadID)
			if err != nil {
				cancelReads(err)
			}
		})
	}
	for i := range identities {
		identity := &identities[i]
		if _, needed := hostBindings[identity.binding]; !needed {
			continue
		}
		reads.Go(func() {
			var err error
			identity.principal, err = p.readBYOIdentityPrincipal(readCtx, identity.id)
			if err != nil {
				cancelReads(err)
			}
		})
	}
	reads.Wait()

	if err := context.Cause(readCtx); err != nil {
		return err
	}
	resolvedBindings := map[string]string{
		"federatedIdentityRoleDefinitionId": federationRoleID,
		"kubeletObjectId":                   kubeletObjectID,
	}
	for _, identity := range identities {
		if _, needed := hostBindings[identity.binding]; !needed {
			continue
		}
		resolvedBindings[identity.binding] = identity.principal
	}
	for name := range hostBindings {
		hostBindings[name] = map[string]any{"value": resolvedBindings[name]}
	}
	source.parameters = mergeParameters(source.parameters, hostBindings)
	return nil
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
		!strings.EqualFold(id.ResourceType.String(), kind) || len(strings.Split(value, "/")) != 9 {
		return nil, fmt.Errorf("expected a complete %s resource ID", kind)
	}
	return id, nil
}

func (p *FoundryProvisioningProvider) readBYOKubeletObjectID(
	ctx context.Context, clusterID string,
) (string, error) {
	id, err := parseBYOResourceID(clusterID, "Microsoft.ContainerService/managedClusters")
	if err != nil {
		return "", err
	}
	options := byoARMOptions(p.armClientOptions)
	clusters, err := armcontainerservice.NewManagedClustersClient(id.SubscriptionID, p.credential, options)
	if err != nil {
		return "", fmt.Errorf("create AKS client: %w", err)
	}
	cluster, err := clusters.Get(ctx, id.ResourceGroupName, id.Name, nil)
	if err != nil {
		return "", fmt.Errorf("read AKS kubelet identity: %w", err)
	}
	if cluster.Properties == nil || cluster.Properties.IdentityProfile["kubeletidentity"] == nil ||
		cluster.Properties.IdentityProfile["kubeletidentity"].ObjectID == nil {
		return "", fmt.Errorf("BYO AKS requires a kubelet principal object ID")
	}
	kubeletObjectID := *cluster.Properties.IdentityProfile["kubeletidentity"].ObjectID
	if _, err := uuid.Parse(kubeletObjectID); err != nil {
		return "", fmt.Errorf("AKS kubelet principal object ID must be a GUID: %w", err)
	}
	return kubeletObjectID, nil
}

func (p *FoundryProvisioningProvider) resolveBYOFederationRoleID(
	ctx context.Context, workloadID string,
) (string, error) {
	const roleName = "Managed Identity Federated Identity Credential Contributor"
	roleSubscription := p.subID
	if workloadID != "" {
		workload, err := parseBYOResourceID(workloadID, "Microsoft.ManagedIdentity/userAssignedIdentities")
		if err != nil {
			return "", err
		}
		roleSubscription = workload.SubscriptionID
	}
	roles, err := armauthorization.NewRoleDefinitionsClient(p.credential, byoARMOptions(p.armClientOptions))
	if err != nil {
		return "", fmt.Errorf("create role definitions client: %w", err)
	}
	pager := roles.NewListPager("/subscriptions/"+roleSubscription,
		&armauthorization.RoleDefinitionsClientListOptions{Filter: new("roleName eq '" + roleName + "'")})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("resolve built-in federation role: %w", err)
		}
		for _, role := range page.Value {
			if role == nil || role.Properties == nil || role.Properties.RoleName == nil ||
				*role.Properties.RoleName != roleName || role.Properties.RoleType == nil ||
				!strings.EqualFold(*role.Properties.RoleType, "BuiltInRole") {
				continue
			}
			if role.ID == nil {
				return "", fmt.Errorf("built-in federation role has an invalid ID")
			}
			roleID, err := arm.ParseResourceID(*role.ID)
			if err != nil || !strings.EqualFold(roleID.ResourceType.String(), "Microsoft.Authorization/roleDefinitions") {
				return "", fmt.Errorf("built-in federation role has an invalid ID")
			}
			if _, err := uuid.Parse(roleID.Name); err != nil {
				return "", fmt.Errorf("BYO AKS federation role requires a role definition GUID: %w", err)
			}
			return "/subscriptions/" + roleSubscription +
				"/providers/Microsoft.Authorization/roleDefinitions/" + roleID.Name, nil
		}
	}
	return "", fmt.Errorf("required built-in role %q is unavailable", roleName)
}

func byoHostingParameter(source *templateSource) (byoHostingParameters, error) {
	var hosting byoHostingParameters
	var value any
	declared := false
	definitions, _ := source.armTemplate["parameters"].(map[string]any)
	for name, definition := range definitions {
		if strings.EqualFold(name, "agentHosting") {
			declared = true
			if definition, ok := definition.(map[string]any); ok {
				value = definition["defaultValue"]
			}
		}
	}
	if !declared {
		return hosting, nil
	}
	supplied := false
	for name, parameter := range source.parameters {
		if !strings.EqualFold(name, "agentHosting") {
			continue
		}
		if supplied {
			return hosting, exterrors.Validation(exterrors.CodeConflictingArguments,
				"multiple agentHosting deployment parameters differ only by case",
				"supply agentHosting only once using the template's parameter name")
		}
		supplied = true
		envelope, ok := parameter.(map[string]any)
		literal, present := envelope["value"]
		if !ok || !present || len(envelope) != 1 {
			return hosting, exterrors.Validation(exterrors.CodeInvalidParameter,
				"agentHosting deployment parameter must use a value envelope",
				"supply agentHosting as a literal object with the values needed for local AKS binding lookups")
		}
		value = literal
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return hosting, fmt.Errorf("encode agentHosting deployment parameters: %w", err)
	}
	if err := json.Unmarshal(raw, &hosting); err != nil {
		return hosting, exterrors.Validation(exterrors.CodeInvalidParameter,
			fmt.Sprintf("invalid agentHosting deployment parameters: %s", err),
			"supply agentHosting as a literal object with the values needed for local AKS binding lookups")
	}
	return hosting, nil
}

func byoParameterValue(source *templateSource, key string) any {
	for name, parameter := range source.parameters {
		if strings.EqualFold(name, key) {
			envelope, _ := parameter.(map[string]any)
			return envelope["value"]
		}
	}
	if definitions, ok := source.armTemplate["parameters"].(map[string]any); ok {
		for name, definition := range definitions {
			if strings.EqualFold(name, key) {
				definition, _ := definition.(map[string]any)
				return definition["defaultValue"]
			}
		}
	}
	return nil
}
