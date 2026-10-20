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

func (p *FoundryProvisioningProvider) prepareBYO(
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
	registryID, err := p.optionalRegistryID(ctx)
	if err != nil {
		return err
	}
	subnetID := hosting.AgentSubnetResourceID
	if subnetID == "" {
		vnetID, _ := byoParameterValue(source, "vnetId").(string)
		subnetName, _ := byoParameterValue(source, "agentSubnetName").(string)
		createSubnet, _ := byoParameterValue(source, "createAgentSubnet").(bool)
		managed, _ := byoParameterValue(source, "useManagedEgress").(bool)
		if vnetID != "" && subnetName != "" && !createSubnet && !managed {
			subnetID = vnetID + "/subnets/" + subnetName
		}
	}
	plan, err := onboarding.BuildPlan(onboarding.Input{
		ClusterID: hosting.ClusterResourceID, StorageID: hosting.StorageAccountResourceID,
		RegistryID: registryID, AgentSubnetID: subnetID,
		SubscriptionID: p.subID, ResourceGroup: p.rgName, Location: p.location,
		Environment: p.envName, ProjectPath: p.projectPath,
	})
	if err != nil {
		return err
	}
	plan.NetworkChanges = selected.NetworkChanges
	pipeline := p.byoPipeline
	if pipeline == nil {
		if err := p.ensureCredential(ctx); err != nil {
			return err
		}
		pipeline = &onboarding.Pipeline{
			Resources: onboarding.NewResourceChecker(p.credential, p.armClientOptions),
			Access:    onboarding.NewAuthorizer(p.credential, p.armClientOptions),
			Prepare:   onboarding.NewPreparer(p.credential, p.armClientOptions, environmentJournal{p}),
			Resolve: func(ctx context.Context, plan onboarding.Plan) (onboarding.Plan, error) {
				resolved, err := onboarding.ResolveExistingParents(ctx, plan, p.credential, p.armClientOptions)
				if err != nil {
					return plan, err
				}
				return onboarding.ResolveDefaultNetwork(
					ctx, resolved, p.credential, p.armClientOptions, environmentJournal{p})
			},
		}
	}
	runPipeline := *pipeline
	lifecycle := p.ownedBYOLifecycle()
	runPipeline.Resources = byoLifecycleChecker{
		checker: pipeline.Resources, lifecycle: lifecycle,
		spec: func(plan onboarding.Plan, facts onboarding.Facts) (onboarding.FoundrySpec, error) {
			spec, err := byoFoundrySpec(plan, source, facts.RegistryABAC)
			if err == nil {
				p.byoSpec = spec
			}
			return spec, err
		},
	}
	runPipeline.Resolve = func(ctx context.Context, plan onboarding.Plan) (onboarding.Plan, error) {
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
	runPipeline.Observe = func(report onboarding.Report) {
		if pipeline.Observe != nil {
			pipeline.Observe(report)
		}
		var formatted strings.Builder
		_ = report.Write(&formatted)
		progress(formatted.String())
	}
	_, err = runPipeline.Run(ctx, plan, preview, func(ctx context.Context, prepared onboarding.Prepared) error {
		if err := synthesis.ApplyPreparedHosting(p.synthResult, synthesis.PreparedHosting{
			HostingManagementIdentityResourceID: prepared.Hosting.ID,
			WorkloadIdentityResourceID:          prepared.Workload.ID,
			StorageAccountResourceID:            prepared.StorageID,
			AgentSubnetResourceID:               prepared.AgentSubnetID,
			RegistryResourceID:                  prepared.RegistryID,
			RegistryABAC:                        prepared.RegistryABAC,
		}); err != nil {
			return err
		}
		if prepared.ResourceGroupTags != nil {
			tags := map[string]string{"azd-env-name": p.envName}
			for key, value := range prepared.ResourceGroupTags {
				if value != nil {
					tags[key] = *value
				}
			}
			p.synthResult.Parameters["tags"] = tags
		}
		p.synthResult.Parameters["byoAccountName"] = onboarding.PlannedFoundryAccount(plan).Name
		p.synthResult.Parameters["byoOwner"] = plan.Owner
		p.synthResult.Parameters["byoRegistryAccessReady"] = false
		return ctx.Err()
	})
	return err
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
	selection := onboarding.DeploymentSelection{PrincipalType: "User"}
	if raw := byoParameterValue(source, "deployments"); raw != nil {
		data, err := json.Marshal(raw)
		if err != nil {
			return selection, fmt.Errorf("P09: read effective model deployments: %w", err)
		}
		var models []json.RawMessage
		if err := json.Unmarshal(data, &models); err != nil {
			return selection, fmt.Errorf("P09: effective deployments must be an array: %w", err)
		}
		selection.Models = len(models) > 0
	}
	for key, out := range map[string]*string{
		"principalId": &selection.PrincipalID, "principalType": &selection.PrincipalType,
	} {
		if raw := byoParameterValue(source, key); raw != nil {
			text, ok := raw.(string)
			if !ok {
				return selection, fmt.Errorf("P09: effective %s must be a string", key)
			}
			*out = text
		}
	}
	for _, key := range []string{"enableNetworkIsolation", "createAgentSubnet", "useManagedEgress"} {
		if raw := byoParameterValue(source, key); raw != nil {
			enabled, ok := raw.(bool)
			if !ok {
				return selection, fmt.Errorf("P09: effective %s must be a boolean", key)
			}
			selection.NetworkChanges = selection.NetworkChanges || enabled
		}
	}
	return selection, nil
}

func (p *FoundryProvisioningProvider) bindPreparedGroupTags(parameters map[string]any) error {
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
