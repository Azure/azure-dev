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
	"azure.ai.projects/internal/onboarding"
	"azure.ai.projects/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/google/uuid"
)

func requireBYOAKSFeature() error {
	enabled, err := strconv.ParseBool(os.Getenv(synthesis.BYOAKSEnvVar))
	if err == nil && enabled {
		return nil
	}
	return exterrors.Compatibility(
		exterrors.CodeFeatureNotEnabled,
		fmt.Sprintf("preview feature %q is disabled", synthesis.BYOAKSFeatureID),
		fmt.Sprintf("enable by setting %s=true in your shell or CI before running azd", synthesis.BYOAKSEnvVar),
	)
}

func (p *FoundryProvisioningProvider) prepareBYO(
	ctx context.Context, source *templateSource, progress grpcbroker.ProgressFunc,
) error {
	if err := requireBYOAKSFeature(); err != nil {
		return err
	}
	includeAcr, ok := byoParameterValue(source, "includeAcr").(bool)
	if !ok || !includeAcr {
		return fmt.Errorf("BYO AKS requires includeAcr=true for the native azd-created Container Registry")
	}
	var hosting struct {
		Enabled           bool   `json:"enabled"`
		HostingType       string `json:"hostingType"`
		Name              string `json:"name"`
		ClusterResourceID string `json:"clusterResourceId"`
	}
	raw, err := json.Marshal(byoParameterValue(source, "agentHosting"))
	if err != nil {
		return fmt.Errorf("encode agentHosting deployment parameters: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&hosting); err != nil {
		return fmt.Errorf("invalid agentHosting deployment parameters: %w", err)
	}
	if !hosting.Enabled || hosting.HostingType != "ManagedCluster" || hosting.Name != "default" ||
		!strings.EqualFold(hosting.ClusterResourceID, p.synthResult.Hosting.ClusterResourceID) {
		return fmt.Errorf("agentHosting deployment parameters conflict with azure.yaml; " +
			"only clusterResourceId is configurable and azd creates its prerequisites")
	}
	network, err := byoNetworkSelection(source)
	if err != nil {
		return err
	}
	if err := network.Validate(); err != nil {
		return err
	}
	resourceGroup, ok := byoParameterValue(source, "resourceGroupName").(string)
	if !ok || strings.TrimSpace(resourceGroup) == "" {
		return fmt.Errorf("effective resourceGroupName must be a nonempty string")
	}
	if err := p.ensureCredential(ctx); err != nil {
		return err
	}
	checker := p.byoChecker
	if checker == nil {
		checker = onboarding.NewResourceChecker(p.credential, p.armClientOptions)
	}
	facts, report, checkErr := checker.Check(ctx, onboarding.Input{
		ClusterID: hosting.ClusterResourceID, SubscriptionID: p.subID, Network: network,
	})
	var formatted strings.Builder
	if err := report.Write(&formatted); err != nil {
		return err
	}
	if formatted.Len() != 0 {
		progress(formatted.String())
	}
	if checkErr != nil {
		return checkErr
	}
	role, err := arm.ParseResourceID(facts.FederatedIdentityRoleDefinitionID)
	if _, kubeletErr := uuid.Parse(facts.KubeletObjectID); kubeletErr != nil || err != nil ||
		!strings.EqualFold(role.ResourceType.String(), "Microsoft.Authorization/roleDefinitions") ||
		!strings.EqualFold(role.SubscriptionID, p.subID) || role.ResourceGroupName != "" {
		return fmt.Errorf("BYO AKS requires a kubelet principal and a built-in federation role")
	}
	if _, err := uuid.Parse(role.Name); err != nil {
		return fmt.Errorf("BYO AKS federation role requires a role definition GUID: %w", err)
	}
	_, resourceGroupExists, err := p.lookupResourceGroupState(ctx, resourceGroup)
	if err != nil {
		return err
	}
	if resourceGroupExists && !supportsBYOResourceGroupReuse(source.armTemplate) {
		return exterrors.Validation(exterrors.CodeInvalidParameter,
			"the BYO AKS template cannot safely reuse the existing resource group",
			"declare a createResourceGroup boolean parameter and guard the resource-group declaration with "+
				"if (createResourceGroup), or re-eject the infrastructure templates")
	}
	source.parameters = mergeParameters(source.parameters, parametersDeclaredByTemplate(map[string]any{
		"kubeletObjectId":                   map[string]any{"value": facts.KubeletObjectID},
		"federatedIdentityRoleDefinitionId": map[string]any{"value": facts.FederatedIdentityRoleDefinitionID},
	}, source.armTemplate))
	definitions, _ := source.armTemplate["parameters"].(map[string]any)
	if _, declared := definitions["createResourceGroup"]; declared {
		source.parameters["createResourceGroup"] = map[string]any{"value": !resourceGroupExists}
	}
	return nil
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

func byoNetworkSelection(source *templateSource) (onboarding.Network, error) {
	var network onboarding.Network
	if enabled, ok := byoParameterValue(source, "enableNetworkIsolation").(bool); !ok || !enabled {
		return network, fmt.Errorf("BYO AKS requires network.agentSubnet and network.peSubnet")
	}
	if managed, ok := byoParameterValue(source, "useManagedEgress").(bool); !ok || managed {
		return network, fmt.Errorf("BYO AKS does not support Microsoft-managed egress; specify network.agentSubnet")
	}
	for key, target := range map[string]*string{
		"vnetId": &network.VNetID, "agentSubnetName": &network.AgentSubnetName,
		"agentSubnetPrefix": &network.AgentSubnetPrefix, "peSubnetName": &network.PESubnetName,
		"peSubnetPrefix":        &network.PESubnetPrefix,
		"dnsZonesResourceGroup": &network.DNSResourceGroup, "dnsZonesSubscription": &network.DNSSubscription,
	} {
		value, ok := byoParameterValue(source, key).(string)
		if !ok {
			return network, fmt.Errorf("effective %s must be a string", key)
		}
		*target = value
	}
	for key, target := range map[string]*bool{
		"createAgentSubnet": &network.CreateAgentSubnet, "createPESubnet": &network.CreatePESubnet,
	} {
		value, ok := byoParameterValue(source, key).(bool)
		if !ok {
			return network, fmt.Errorf("effective %s must be a boolean", key)
		}
		*target = value
	}
	if mode, ok := byoParameterValue(source, "managedIsolationMode").(string); !ok || mode != "" {
		return network, fmt.Errorf("managedIsolationMode cannot take effect with BYO AKS")
	}
	return network, nil
}

func byoDestroyError() error {
	return exterrors.Validation(exterrors.CodeInvalidParameter,
		"azd down is not supported for BYO AKS deployments; no resources were deleted",
		"use an explicit, resource-scoped cleanup; "+
			"do not delete the resource group containing the existing AKS or networking resources")
}

func (p *FoundryProvisioningProvider) refuseBYODeploymentDestroy(ctx context.Context) error {
	client, err := p.deploymentsClient(ctx)
	if err != nil {
		return err
	}
	deployment, err := client.GetAtSubscriptionScope(ctx, p.deploymentName(), nil)
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return exterrors.ServiceFromAzure(err, exterrors.OpArmDeploymentGet)
	}
	if deployment.Properties == nil {
		return fmt.Errorf("deployment properties are unavailable; cannot safely delete the resource group")
	}
	parameters, ok := deployment.Properties.Parameters.(map[string]any)
	if !ok {
		return fmt.Errorf("deployment parameters are unavailable; cannot safely delete the resource group")
	}
	for name, value := range parameters {
		if !strings.EqualFold(name, "agentHosting") {
			continue
		}
		envelope, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("agentHosting deployment parameter is unavailable; cannot safely delete the resource group")
		}
		hosting, ok := envelope["value"].(map[string]any)
		if !ok {
			return fmt.Errorf("agentHosting deployment parameter is invalid; cannot safely delete the resource group")
		}
		enabled, ok := hosting["enabled"].(bool)
		if !ok || enabled {
			return byoDestroyError()
		}
	}
	return nil
}
