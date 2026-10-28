// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/onboarding"

	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
)

type byoOnboardingParameters struct {
	DeploymentID                      string `json:"deploymentId"`
	ResourceGroupSupplied             bool   `json:"resourceGroupSupplied"`
	StorageName                       string `json:"storageName"`
	StorageSKU                        string `json:"storageSku"`
	StorageAccountResourceID          string `json:"storageAccountResourceId"`
	HostingIdentityName               string `json:"hostingIdentityName"`
	HostingIdentityResourceID         string `json:"hostingIdentityResourceId"`
	WorkloadIdentityName              string `json:"workloadIdentityName"`
	WorkloadIdentityResourceID        string `json:"workloadIdentityResourceId"`
	KubeletObjectID                   string `json:"kubeletObjectId"`
	FederatedIdentityRoleDefinitionID string `json:"federatedIdentityRoleDefinitionId"`
}

func (p *FoundryProvisioningProvider) prepareBYO(
	ctx context.Context, source *templateSource, progress grpcbroker.ProgressFunc,
) (*byoState, error) {
	previous, err := p.ownedBYOJournal().Load(ctx)
	if err != nil {
		return nil, err
	}
	if previous != nil && previous.Status != "succeeded" {
		return nil, fmt.Errorf("the previous BYO AKS deployment is unresolved; retain %s for manual recovery; "+
			"automatic retry and adoption are not supported", envKeyBYOJournal)
	}
	templateDigest, err := standardBYOTemplateDigest(source.armTemplate)
	if err != nil {
		return nil, err
	}
	if p.onDiskSource != nil {
		if _, supplied := p.onDiskSource.parameters["byoOnboarding"]; supplied {
			return nil, fmt.Errorf("byoOnboarding is managed internally; remove the explicit deployment parameter")
		}
	}
	for key, expected := range map[string]string{"resourceGroupName": p.rgName, "location": p.location} {
		value, ok := byoParameterValue(source, key).(string)
		if !ok || !strings.EqualFold(value, expected) {
			return nil, fmt.Errorf("BYO AKS parameter %s conflicts with the resolved environment target", key)
		}
	}
	includeAcr, ok := byoParameterValue(source, "includeAcr").(bool)
	if !ok || !includeAcr {
		return nil, fmt.Errorf("BYO AKS requires includeAcr=true for the native azd-created Container Registry")
	}
	actualHosting, err := onboarding.Digest(byoParameterValue(source, "agentHosting"))
	if err != nil {
		return nil, err
	}
	expectedHosting, err := onboarding.Digest(p.synthResult.Parameters["agentHosting"])
	if err != nil {
		return nil, err
	}
	if actualHosting != expectedHosting {
		return nil, fmt.Errorf("agentHosting deployment parameters conflict with azure.yaml; " +
			"only clusterResourceId is configurable and azd creates its prerequisites")
	}
	network, err := byoNetworkSelection(source)
	if err != nil {
		return nil, err
	}
	if err := network.Validate(); err != nil {
		return nil, err
	}
	project, ok := byoParameterValue(source, "foundryProjectName").(string)
	if !ok || len(project) < 3 || len(project) > 32 || strings.ContainsAny(project, "/\\?#%${} \t\r\n") {
		return nil, fmt.Errorf("effective foundryProjectName must be a valid 3-32 character project name")
	}
	var tags map[string]string
	rawTags, err := json.Marshal(byoParameterValue(source, "tags"))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(rawTags, &tags); err != nil {
		return nil, fmt.Errorf("BYO AKS tags must map strings to strings: %w", err)
	}
	if tags == nil {
		tags = map[string]string{}
	}
	deploymentID := "/subscriptions/" + p.subID + "/providers/Microsoft.Resources/deployments/" + p.deploymentName()
	for key, expected := range map[string]string{"azd-env-name": p.envName, byoOwnerTag: deploymentID} {
		for actual, value := range tags {
			if strings.EqualFold(actual, key) {
				if value != expected {
					return nil, fmt.Errorf("tag %s conflicts with the BYO AKS deployment identity", key)
				}
				delete(tags, actual)
			}
		}
		tags[key] = expected
	}
	registry, err := p.byoEnvValue(ctx, "AZURE_CONTAINER_REGISTRY_RESOURCE_ID")
	if err != nil {
		return nil, fmt.Errorf("read registry environment output: %w", err)
	}
	if registry != "" && (previous == nil || !strings.EqualFold(registry, previous.Plan.Registry.ID)) {
		return nil, exterrors.Validation(exterrors.CodeInvalidParameter,
			"AZURE_CONTAINER_REGISTRY_RESOURCE_ID cannot select an existing registry for BYO AKS",
			"use an environment without a supplied registry; BYO AKS creates the native azd registry")
	}
	names, err := p.resolveBYONames(ctx, source)
	if err != nil {
		return nil, err
	}
	if previous != nil {
		names.ResourceGroupSupplied = previous.Plan.ResourceGroupSupplied
	}
	if names.DeploymentID != deploymentID {
		return nil, fmt.Errorf("native naming returned a different subscription deployment target")
	}
	plan, err := onboarding.BuildPlan(onboarding.Input{
		ClusterID:      p.synthResult.Hosting.ClusterResourceID,
		SubscriptionID: p.subID, ResourceGroup: p.rgName, Location: p.location, Environment: p.envName,
		AccountID: names.AccountID, ProjectName: project, RegistryName: names.RegistryName,
		DeploymentID: names.DeploymentID, ResourceGroupSupplied: names.ResourceGroupSupplied, Network: network,
	})
	if err != nil {
		return nil, err
	}
	if previous != nil &&
		(previous.Version != 1 || previous.Plan != plan || previous.TemplateDigest != templateDigest) {
		return nil, fmt.Errorf("BYO AKS target, network or template differs from its completed deployment intent; " +
			"changing the resource selection is not supported")
	}
	if err := p.ensureCredential(ctx); err != nil {
		return nil, err
	}
	checker := p.byoChecker
	if checker == nil {
		checker = onboarding.NewResourceChecker(p.credential, p.armClientOptions)
	}
	facts, report, checkErr := checker.Check(ctx, plan)
	var formatted strings.Builder
	if err := report.Write(&formatted); err != nil {
		return nil, err
	}
	if formatted.Len() != 0 {
		progress(formatted.String())
	}
	if checkErr != nil {
		return nil, checkErr
	}
	if err := validateBYOFacts(plan, facts); err != nil {
		return nil, err
	}
	bindings := byoOnboardingParameters{
		DeploymentID: plan.DeploymentID, ResourceGroupSupplied: plan.ResourceGroupSupplied,
		StorageName: plan.Storage.Name, StorageSKU: onboarding.StorageSKU(plan.Location),
		StorageAccountResourceID: plan.Storage.ID,
		HostingIdentityName:      plan.Hosting.Name, HostingIdentityResourceID: plan.Hosting.ID,
		WorkloadIdentityName: plan.Workload.Name, WorkloadIdentityResourceID: plan.Workload.ID,
		KubeletObjectID: facts.KubeletObjectID, FederatedIdentityRoleDefinitionID: facts.FederatedIdentityRoleDefinitionID,
	}
	source.parameters["byoOnboarding"] = map[string]any{"value": bindings}
	source.parameters["tags"] = map[string]any{"value": tags}
	bindingDigest, err := byoBindingDigest(source.parameters)
	if err != nil {
		return nil, err
	}
	if previous != nil && bindingDigest != previous.BindingDigest {
		return nil, fmt.Errorf("BYO AKS immutable deployment bindings differ from the completed intent")
	}
	if err := p.verifyBYODeployment(ctx, plan, previous); err != nil {
		return nil, err
	}
	origins, err := p.observeBYOTargets(ctx, plan, previous)
	if err != nil {
		return nil, err
	}
	return &byoState{
		Version: 1, Status: "deploying", Plan: plan, TemplateDigest: templateDigest,
		BindingDigest: bindingDigest, Origins: origins,
		Facts: facts,
	}, nil
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
