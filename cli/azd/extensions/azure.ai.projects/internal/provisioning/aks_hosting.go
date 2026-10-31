// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/google/uuid"
)

func requireBYOAKSFeature() error {
	value, present := os.LookupEnv(synthesis.BYOAKSEnvVar)
	if !present {
		return nil
	}
	enabled, err := strconv.ParseBool(value)
	if err == nil && enabled {
		return nil
	}
	return exterrors.Compatibility(
		exterrors.CodeFeatureNotEnabled,
		fmt.Sprintf("preview feature %q is disabled", synthesis.BYOAKSFeatureID),
		fmt.Sprintf("enable by setting %s=true in your shell or CI before running azd", synthesis.BYOAKSEnvVar),
	)
}

func (p *FoundryProvisioningProvider) prepareBYO(ctx context.Context, source *templateSource) error {
	configured := p.synthResult != nil && p.synthResult.Hosting != nil
	if configured {
		if err := requireBYOAKSFeature(); err != nil {
			return err
		}
	}
	value, err := byoHostingParameter(source)
	if err != nil {
		return err
	}
	var hosting struct {
		Enabled           bool   `json:"enabled"`
		HostingType       string `json:"hostingType"`
		Name              string `json:"name"`
		ClusterResourceID string `json:"clusterResourceId"`
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
			return nil
		}
		if err := requireBYOAKSFeature(); err != nil {
			return err
		}
		return exterrors.Validation(exterrors.CodeConflictingArguments,
			"effective agentHosting deployment parameters enable BYO AKS without agentHosting in azure.yaml",
			"set agentHosting.clusterResourceId on the azure.ai.project service in azure.yaml, "+
				"or disable agentHosting in the parameters file and template defaults")
	}
	includeAcr, ok := byoParameterValue(source, "includeAcr").(bool)
	if !ok || !includeAcr {
		return fmt.Errorf("BYO AKS requires includeAcr=true for the native azd-created Container Registry")
	}
	if !hosting.Enabled || hosting.HostingType != "ManagedCluster" || hosting.Name != "default" ||
		!strings.EqualFold(hosting.ClusterResourceID, p.synthResult.Hosting.ClusterResourceID) {
		return fmt.Errorf("agentHosting deployment parameters conflict with azure.yaml; " +
			"only clusterResourceId is configurable and azd creates its prerequisites")
	}
	resourceGroup, ok := byoParameterValue(source, "resourceGroupName").(string)
	if !ok || strings.TrimSpace(resourceGroup) == "" {
		return fmt.Errorf("effective resourceGroupName must be a nonempty string")
	}
	var requestedResourceGroupCreation *bool
	var resourceGroupCreationParameter string
	for name, parameter := range source.parameters {
		if !strings.EqualFold(name, "createResourceGroup") {
			continue
		}
		if requestedResourceGroupCreation != nil {
			return exterrors.Validation(exterrors.CodeConflictingArguments,
				"multiple createResourceGroup deployment parameters differ only by case",
				"supply createResourceGroup only once using the template's parameter name")
		}
		envelope, _ := parameter.(map[string]any)
		value, ok := envelope["value"].(bool)
		if !ok {
			return exterrors.Validation(exterrors.CodeInvalidParameter,
				"createResourceGroup must be a boolean",
				"set createResourceGroup to true or false in the parameters file, or omit it for automatic selection")
		}
		requestedResourceGroupCreation = new(value)
		resourceGroupCreationParameter = name
	}
	suppliedBindings := map[string]string{}
	for name, parameter := range source.parameters {
		if !strings.EqualFold(name, "kubeletObjectId") && !strings.EqualFold(name, "federatedIdentityRoleDefinitionId") {
			continue
		}
		envelope, _ := parameter.(map[string]any)
		value, ok := envelope["value"].(string)
		if !ok || strings.TrimSpace(value) == "" {
			return exterrors.Validation(exterrors.CodeInvalidParameter,
				fmt.Sprintf("%s must be a nonempty string matching the provider-resolved BYO AKS binding", name),
				fmt.Sprintf("remove %s from the parameters file so microsoft.foundry can resolve it automatically", name))
		}
		suppliedBindings[name] = value
	}
	if err := p.ensureCredential(ctx); err != nil {
		return err
	}
	kubeletObjectID, federationRoleID, err := p.resolveBYOHostBindings(ctx, hosting.ClusterResourceID)
	if err != nil {
		return err
	}
	for name, value := range suppliedBindings {
		expected := kubeletObjectID
		if strings.EqualFold(name, "federatedIdentityRoleDefinitionId") {
			expected = federationRoleID
		}
		if !strings.EqualFold(value, expected) {
			return exterrors.Validation(exterrors.CodeConflictingArguments,
				fmt.Sprintf("%s conflicts with the provider-resolved binding for agentHosting.clusterResourceId", name),
				fmt.Sprintf("remove %s from the parameters file so microsoft.foundry can resolve it automatically", name))
		}
	}
	_, resourceGroupExists, err := p.lookupResourceGroupState(ctx, resourceGroup)
	if err != nil {
		return err
	}
	if !resourceGroupExists && requestedResourceGroupCreation != nil && !*requestedResourceGroupCreation {
		return exterrors.Validation(exterrors.CodeConflictingArguments,
			fmt.Sprintf("createResourceGroup=false conflicts with missing resource group %q", resourceGroup),
			"select an existing resource group, create it separately, or allow creation with createResourceGroup=true")
	}
	if resourceGroupExists && !supportsBYOResourceGroupReuse(source.armTemplate) {
		return exterrors.Validation(exterrors.CodeInvalidParameter,
			"the BYO AKS template cannot safely reuse the existing resource group",
			"declare a createResourceGroup boolean parameter and guard the resource-group declaration with "+
				"if (createResourceGroup), or re-eject the infrastructure templates")
	}
	hostBindings := parametersDeclaredByTemplate(map[string]any{
		"kubeletObjectId":                   map[string]any{"value": kubeletObjectID},
		"federatedIdentityRoleDefinitionId": map[string]any{"value": federationRoleID},
	}, source.armTemplate)
	for name := range suppliedBindings {
		for binding := range hostBindings {
			if strings.EqualFold(name, binding) {
				delete(source.parameters, name)
			}
		}
	}
	source.parameters = mergeParameters(source.parameters, hostBindings)
	definitions, _ := source.armTemplate["parameters"].(map[string]any)
	if _, declared := definitions["createResourceGroup"]; declared {
		if requestedResourceGroupCreation != nil {
			delete(source.parameters, resourceGroupCreationParameter)
		}
		source.parameters["createResourceGroup"] = map[string]any{"value": !resourceGroupExists}
	}
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
		!strings.EqualFold(id.ResourceType.String(), kind) {
		return nil, fmt.Errorf("expected a complete %s resource ID", kind)
	}
	return id, nil
}

func (p *FoundryProvisioningProvider) resolveBYOHostBindings(
	ctx context.Context, clusterID string,
) (string, string, error) {
	id, err := parseBYOResourceID(clusterID, "Microsoft.ContainerService/managedClusters")
	if err != nil {
		return "", "", err
	}
	options := byoARMOptions(p.armClientOptions)
	clusters, err := armcontainerservice.NewManagedClustersClient(id.SubscriptionID, p.credential, options)
	if err != nil {
		return "", "", fmt.Errorf("create AKS client: %w", err)
	}
	cluster, err := clusters.Get(ctx, id.ResourceGroupName, id.Name, nil)
	if err != nil {
		return "", "", fmt.Errorf("read AKS kubelet identity: %w", err)
	}
	if cluster.Properties == nil || cluster.Properties.IdentityProfile["kubeletidentity"] == nil ||
		cluster.Properties.IdentityProfile["kubeletidentity"].ObjectID == nil {
		return "", "", fmt.Errorf("BYO AKS requires a kubelet principal object ID for registry access")
	}
	kubeletObjectID := *cluster.Properties.IdentityProfile["kubeletidentity"].ObjectID
	if _, err := uuid.Parse(kubeletObjectID); err != nil {
		return "", "", fmt.Errorf("AKS kubelet principal object ID must be a GUID: %w", err)
	}
	const roleName = "Managed Identity Federated Identity Credential Contributor"
	roles, err := armauthorization.NewRoleDefinitionsClient(p.credential, options)
	if err != nil {
		return "", "", fmt.Errorf("create role definitions client: %w", err)
	}
	pager := roles.NewListPager("/subscriptions/"+p.subID,
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
				"/subscriptions/" + p.subID + "/providers/Microsoft.Authorization/roleDefinitions/" + roleID.Name, nil
		}
	}
	return "", "", fmt.Errorf("required built-in role %q is unavailable", roleName)
}

func supportsBYOResourceGroupReuse(template map[string]any) bool {
	definitions, _ := template["parameters"].(map[string]any)
	parameter, _ := definitions["createResourceGroup"].(map[string]any)
	if parameter["type"] != "bool" {
		return false
	}
	safeResource := func(value any) bool {
		resource, ok := value.(map[string]any)
		return ok && (resource["type"] != "Microsoft.Resources/resourceGroups" ||
			resource["condition"] == "[parameters('createResourceGroup')]")
	}
	switch resources := template["resources"].(type) {
	case map[string]any:
		for _, resource := range resources {
			if !safeResource(resource) {
				return false
			}
		}
	case []any:
		for _, resource := range resources {
			if !safeResource(resource) {
				return false
			}
		}
	default:
		return false
	}
	return true
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
