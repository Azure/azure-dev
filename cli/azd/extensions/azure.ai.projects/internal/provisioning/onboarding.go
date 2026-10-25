// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/onboarding"
	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
)

const envKeyBYOJournal = "AZD_FOUNDRY_BYO_AKS_PREPARATION"

type environmentJournal struct{ provider *FoundryProvisioningProvider }

func (j environmentJournal) Load(ctx context.Context) (onboarding.PreparationState, error) {
	var state onboarding.PreparationState
	value, err := j.provider.byoJournalValue(ctx)
	if err != nil {
		return state, fmt.Errorf("read BYO-AKS ownership journal: %w", err)
	}
	if value == "" {
		return state, nil
	}
	if err := json.Unmarshal([]byte(value), &state); err != nil {
		return state, fmt.Errorf("invalid BYO-AKS ownership journal; retain it for recovery: %w", err)
	}
	return state, nil
}

func (j environmentJournal) Save(ctx context.Context, state onboarding.PreparationState) error {
	value, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return j.provider.setEnv(ctx, envKeyBYOJournal, string(value))
}

func (p *FoundryProvisioningProvider) planBYO(
	ctx context.Context, progress grpcbroker.ProgressFunc, preview bool,
) error {
	if p.synthResult == nil || p.synthResult.Hosting == nil {
		p.byoPlan = nil
		raw, err := p.byoJournalValue(ctx)
		if err != nil {
			return err
		}
		if raw != "" {
			return fmt.Errorf("a BYO-AKS ownership journal remains; resolve and archive its lifecycle before changing hosting mode")
		}
		return nil
	}
	hosting := p.synthResult.Hosting
	source, err := p.resolveTemplate(ctx, progress)
	if err != nil {
		return err
	}
	selected, err := byoDeploymentSelection(source)
	if err != nil {
		return err
	}
	for key, expected := range map[string]string{"resourceGroupName": p.rgName, "location": p.location} {
		value, ok := byoParameterValue(source, key).(string)
		if !ok || !strings.EqualFold(value, expected) {
			return fmt.Errorf("BYO-AKS parameter %s conflicts with the resolved environment target; "+
				"make the explicit parameter and environment selections agree", key)
		}
	}
	foundryName, ok := byoParameterValue(source, "foundryProjectName").(string)
	if !ok {
		return fmt.Errorf("I03: effective foundryProjectName must be a string for resource naming")
	}
	registryID, err := p.optionalRegistryID(ctx)
	if err != nil {
		return err
	}
	subnetID := ""
	if network := selected.Network; network != nil && network.Enabled && !network.ManagedEgress {
		subnetID = network.VNetID + "/subnets/" + network.AgentSubnetName
	}
	plan, err := onboarding.BuildPlan(onboarding.Input{
		ClusterID: hosting.ClusterResourceID, StorageID: hosting.StorageAccountResourceID,
		RegistryID: registryID, AgentSubnetID: subnetID,
		SubscriptionID: p.subID, ResourceGroup: p.rgName, Location: p.location,
		Environment: p.envName, ProjectPath: p.projectPath,
		FoundryName: foundryName,
	})
	if err != nil {
		return err
	}
	pipeline := p.byoPipeline
	if pipeline == nil {
		if err := p.ensureCredential(ctx); err != nil {
			return err
		}
		pipeline = &onboarding.Pipeline{
			Resources: onboarding.NewResourceChecker(p.credential, p.armClientOptions),
			Access:    onboarding.NewAuthorizer(p.credential, p.armClientOptions),
			Resolve: func(ctx context.Context, plan onboarding.Plan) (onboarding.Plan, error) {
				return onboarding.ResolveExistingParents(ctx, plan, p.credential, p.armClientOptions)
			},
		}
	}
	runPipeline := *pipeline
	lifecycle := p.ownedBYOLifecycle()
	runPipeline.Resolve = func(ctx context.Context, plan onboarding.Plan) (onboarding.Plan, error) {
		plan, err := onboarding.ResolveRegistrySelection(ctx, plan, environmentJournal{p})
		if err != nil {
			return plan, err
		}
		if pipeline.Resolve != nil {
			resolved, err := pipeline.Resolve(ctx, plan)
			if err != nil {
				return plan, err
			}
			plan = resolved
		}
		resolved, err := onboarding.WithDeploymentOperations(plan, selected)
		if err == nil {
			p.byoPlan = &resolved
		}
		return resolved, err
	}
	observe := func(report onboarding.Report) {
		var formatted strings.Builder
		_ = report.Write(&formatted)
		progress(formatted.String())
	}
	resolved, err := runPipeline.Resolve(ctx, plan)
	if err != nil {
		report := onboarding.Report{{
			ID: "I03", Scope: plan.Cluster.ID, Status: onboarding.Unverified, Mandatory: true,
			Expected: "read-only resolved creation/reuse and existing-ancestor plan",
			Observed: err.Error(), Missing: "supported plan and existing-ancestor evidence",
			Remediation: "resolve the reported read/planning failure before provisioning", Cause: err,
		}}
		observe(report)
		return report.Err()
	}
	facts, report, err := runPipeline.Preflight(ctx, resolved)
	if err != nil {
		observe(report)
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	bindings := p.byoBindings
	if bindings == nil {
		bindings = func(ctx context.Context, plan onboarding.Plan, facts onboarding.Facts) (onboarding.DeploymentData, error) {
			return onboarding.ResolveDeploymentBindings(ctx, p.credential, p.armClientOptions, plan, facts)
		}
	}
	data, err := bindings(ctx, resolved, facts)
	if err != nil {
		report = append(report, onboarding.Check{
			ID: "I03", Scope: resolved.Cluster.ID, Status: onboarding.Unverified, Mandatory: true,
			Expected: "read-only role-definition and create/reuse bindings for the ARM graph",
			Observed: err.Error(), Missing: "complete resolved deployment bindings",
			Remediation: "resolve the reported role/resource read failure before provisioning", Cause: err,
		})
		observe(report)
		return report.Err()
	}
	spec, err := byoFoundrySpec(resolved, source, facts.RegistryABAC)
	if err != nil {
		return err
	}
	spec.Deployment = &data
	p.byoSpec = spec
	ownership, err := lifecycle.Check(ctx, resolved, spec)
	report = append(report, ownership...)
	observe(report)
	if err != nil {
		return err
	}
	if err := report.Err(); err != nil || preview {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.bindBYOPlan(resolved, data)
}

func (p *FoundryProvisioningProvider) bindBYOPlan(plan onboarding.Plan, data onboarding.DeploymentData) error {
	if err := synthesis.ApplyBYOOnboarding(p.synthResult, synthesis.BYOOnboardingParameters{
		Owner: plan.Owner, AccountName: onboarding.PlannedFoundryAccount(plan).Name,
		ResourceGroupSupplied: data.ResourceGroupSupplied,
		ClusterResourceID:     data.ClusterID, StorageAccountResourceID: data.StorageID,
		StorageName: plan.Storage.Name, StorageSupplied: data.StorageSupplied, StorageSKU: data.StorageSKU,
		HostingIdentityName: data.Hosting.Name, WorkloadIdentityName: data.Workload.Name,
		RegistryResourceID: data.RegistryID, RegistryName: plan.Registry.Name,
		RegistrySupplied: data.RegistrySupplied, RegistryABAC: data.RegistryABAC,
		KubeletObjectID: data.KubeletObjectID, FederatedIdentityRoleDefinitionID: data.FederatedIdentityRoleDefinitionID,
		KubeletRoleAssignmentID: data.KubeletRoleAssignmentID,
	}); err != nil {
		return err
	}
	tags := map[string]string{"azd-env-name": p.envName}
	for key, value := range plan.ResourceGroupTags {
		if value != nil {
			tags[key] = *value
		}
	}
	p.synthResult.Parameters["tags"] = tags
	return nil
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

func (p *FoundryProvisioningProvider) bindPlannedGroupTags(parameters map[string]any) error {
	tags, ok := p.synthResult.Parameters["tags"].(map[string]string)
	if !ok {
		return nil
	}
	explicit := map[string]string{}
	if envelope, ok := parameters["tags"].(map[string]any); ok {
		data, err := json.Marshal(envelope["value"])
		if err != nil {
			return fmt.Errorf("encode final resource group tags: %w", err)
		}
		if err := json.Unmarshal(data, &explicit); err != nil {
			return fmt.Errorf("final resource group tags must map strings to strings: %w", err)
		}
	}
	merged := maps.Clone(tags)
	if merged == nil {
		merged = map[string]string{}
	}
	for key, value := range explicit {
		if strings.EqualFold(key, "azd-byoaks-owner") {
			var owner string
			for observedKey, observedValue := range tags {
				if strings.EqualFold(observedKey, key) {
					owner = observedValue
				}
			}
			if owner == "" || value != owner {
				return exterrors.Validation(
					exterrors.CodeInvalidServiceConfig,
					"R01: explicit tags conflict with the private azd-byoaks-owner ownership marker",
					"remove the ownership tag from the template parameters; azd preserves the observed ownership marker",
				)
			}
			continue
		}
		for observedKey := range merged {
			if strings.EqualFold(observedKey, key) {
				delete(merged, observedKey)
			}
		}
		merged[key] = value
	}
	parameters["tags"] = map[string]any{"value": merged}
	return nil
}

func (p *FoundryProvisioningProvider) optionalRegistryID(ctx context.Context) (string, error) {
	const key = "AZURE_CONTAINER_REGISTRY_RESOURCE_ID"
	if value, present := p.virtualEnv[key]; present {
		if strings.TrimSpace(value) == "" {
			return "", fmt.Errorf("%s was explicitly set but is empty", key)
		}
		return value, nil
	}
	if p.azdClient == nil {
		return "", nil
	}
	response, err := p.azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{Name: p.envName})
	if err != nil {
		return "", fmt.Errorf("read optional registry selection: %w", err)
	}
	for _, value := range response.KeyValues {
		if value.Key == key {
			if strings.TrimSpace(value.Value) == "" {
				return "", fmt.Errorf("%s was explicitly set but is empty", key)
			}
			return value.Value, nil
		}
	}
	return "", nil
}
