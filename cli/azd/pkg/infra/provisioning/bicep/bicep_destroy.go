// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// classifyAndDeleteResourceGroups classifies each resource group as owned/external/unknown
// using the 4-tier pipeline, then only deletes owned RGs.
//
// When force is true, classification is bypassed and all RGs are deleted directly,
// preserving the original `--force` semantics.
//
// Log Analytics Workspaces in owned RGs are force-deleted before the RG if purge is enabled,
// since force-delete requires the workspace to still exist.
// Returns the list of deleted RG names and any skipped RG info.
func (p *BicepProvider) classifyAndDeleteResourceGroups(
	ctx context.Context,
	deployment infra.Deployment,
	groupedResources map[string][]*azapi.Resource,
	options provisioning.DestroyOptions,
) (deleted []string, skipped []azapi.ClassifiedSkip, err error) {
	// Extract RG names from the grouped resources map.
	rgNames := make([]string, 0, len(groupedResources))
	for rgName := range groupedResources {
		rgNames = append(rgNames, rgName)
	}

	// When --force is set, bypass classification and delete all RGs immediately.
	// WARNING: This skips ALL safety checks (Tier 1-4). All referenced RGs will be deleted.
	if options.Force() {
		log.Printf(
			"WARNING: --force flag set — bypassing resource group classification. All %d RGs will be deleted.",
			len(rgNames),
		)
		deleted, err = p.deleteRGList(ctx, deployment.SubscriptionId(), rgNames, groupedResources, options)
		return deleted, nil, err
	}

	// Get deployment info for classification (used for logging).
	deploymentInfo, deployInfoErr := deployment.Get(ctx)
	if deployInfoErr == nil {
		log.Printf("classifying resource groups for deployment: %s", deploymentInfo.Name)
	}

	// Get deployment operations (Tier 1 data — single API call).
	var operations []*armresources.DeploymentOperation
	operations, err = deployment.Operations(ctx)
	if err != nil {
		// Operations unavailable — classification will fall to Tier 2/3.
		log.Printf("WARNING: could not fetch deployment operations for classification: %v", err)
		operations = nil
	}

	// Build classification options.
	// Note: ListResourceGroupResources is not wired up because the current ResourceExtended
	// type does not carry resource tags. Tier 4 foreign-resource veto requires tags to work
	// correctly; omitting it avoids false vetoes until the API is updated.
	subscriptionId := deployment.SubscriptionId()
	classifyOpts := azapi.ClassifyOptions{
		Interactive: !p.console.IsNoPromptMode(),
		EnvName:     p.env.Name(),
		GetResourceGroupTags: func(ctx context.Context, rgName string) (map[string]*string, error) {
			return p.getResourceGroupTags(ctx, subscriptionId, rgName)
		},
		ListResourceGroupLocks: func(ctx context.Context, rgName string) ([]*azapi.ManagementLock, error) {
			// Lock checking requires ManagementLockClient; wired up in a follow-up.
			return nil, nil
		},
		Prompter: func(rgName, reason string) (bool, error) {
			return p.console.Confirm(ctx, input.ConsoleOptions{
				Message:      fmt.Sprintf("Delete resource group '%s'? (%s)", rgName, reason),
				DefaultValue: false,
			})
		},
	}

	// Run classification.
	result, err := azapi.ClassifyResourceGroups(ctx, operations, rgNames, classifyOpts)
	if err != nil {
		return nil, nil, fmt.Errorf("classifying resource groups: %w", err)
	}

	// Log classification results (user-facing display handled by caller).
	for _, skip := range result.Skipped {
		log.Printf("classify rg=%s decision=skip reason=%q", skip.Name, skip.Reason)
	}
	for _, owned := range result.Owned {
		log.Printf("classify rg=%s decision=owned", owned)
	}

	deleted, err = p.deleteRGList(ctx, subscriptionId, result.Owned, groupedResources, options)
	return deleted, result.Skipped, err
}

// deleteRGList deletes a list of resource groups, force-deleting Log Analytics Workspaces first
// in each RG when purge is enabled.
func (p *BicepProvider) deleteRGList(
	ctx context.Context,
	subscriptionId string,
	rgNames []string,
	groupedResources map[string][]*azapi.Resource,
	options provisioning.DestroyOptions,
) (deleted []string, err error) {
	var deleteErrors []error
	for _, rgName := range rgNames {
		// Force-delete Log Analytics Workspaces in this RG before deleting the RG.
		// This must happen while the workspace still exists; force-delete is not possible after.
		if options.Purge() {
			rgResources := map[string][]*azapi.Resource{rgName: groupedResources[rgName]}
			workspaces, wsErr := p.getLogAnalyticsWorkspacesToPurge(ctx, rgResources)
			if wsErr != nil {
				log.Printf("WARNING: could not list log analytics workspaces for rg=%s: %v", rgName, wsErr)
			} else if len(workspaces) > 0 {
				if fdErr := p.forceDeleteLogAnalyticsWorkspaces(ctx, workspaces); fdErr != nil {
					log.Printf("WARNING: force-deleting log analytics workspaces in rg=%s: %v", rgName, fdErr)
				}
			}
		}

		p.console.ShowSpinner(
			ctx,
			fmt.Sprintf("Deleting resource group %s", output.WithHighLightFormat(rgName)),
			input.Step,
		)

		if delErr := p.resourceService.DeleteResourceGroup(ctx, subscriptionId, rgName); delErr != nil {
			p.console.StopSpinner(
				ctx,
				fmt.Sprintf("Failed deleting resource group %s", output.WithHighLightFormat(rgName)),
				input.StepFailed,
			)
			deleteErrors = append(deleteErrors, fmt.Errorf("deleting resource group %s: %w", rgName, delErr))
			continue
		}

		p.console.StopSpinner(
			ctx,
			fmt.Sprintf("Deleted resource group %s", output.WithHighLightFormat(rgName)),
			input.StepDone,
		)
		deleted = append(deleted, rgName)
	}

	if len(deleteErrors) > 0 {
		return deleted, errors.Join(deleteErrors...)
	}
	return deleted, nil
}

// getResourceGroupTags retrieves the tags for a resource group using the ARM API.
// It uses the service locator to resolve the credential provider and ARM client options.
// Returns nil tags (no error) as a graceful fallback if dependencies cannot be resolved,
// which causes the classifier to fall back to Tier 2/3.
func (p *BicepProvider) getResourceGroupTags(
	ctx context.Context,
	subscriptionId string,
	rgName string,
) (map[string]*string, error) {
	var credProvider account.SubscriptionCredentialProvider
	if err := p.serviceLocator.Resolve(&credProvider); err != nil {
		log.Printf("classify tags: credential provider unavailable for rg=%s: %v", rgName, err)
		return nil, nil // graceful fallback: no tags → classifier uses Tier 2/3
	}

	var armOpts *arm.ClientOptions
	_ = p.serviceLocator.Resolve(&armOpts) // optional; nil is a valid default

	credential, err := credProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		log.Printf("classify tags: credential error for rg=%s sub=%s: %v", rgName, subscriptionId, err)
		return nil, nil // graceful fallback
	}

	client, err := armresources.NewResourceGroupsClient(subscriptionId, credential, armOpts)
	if err != nil {
		log.Printf("classify tags: ARM client error for rg=%s: %v", rgName, err)
		return nil, nil // graceful fallback
	}

	resp, err := client.Get(ctx, rgName, nil)
	if err != nil {
		return nil, err // propagate so caller can handle 404/403
	}

	return resp.Tags, nil
}

// voidDeploymentState voids the deployment state by deploying an empty template.
// This ensures subsequent azd provision commands work correctly after a destroy,
// by establishing a new baseline deployment.
func (p *BicepProvider) voidDeploymentState(ctx context.Context, deployment infra.Deployment) error {
	p.console.ShowSpinner(ctx, "Voiding deployment state...", input.Step)

	optionsMap, err := convert.ToMap(p.options)
	if err != nil {
		p.console.StopSpinner(ctx, "Failed to void deployment state", input.StepFailed)
		return err
	}

	if err := deployment.VoidState(ctx, optionsMap); err != nil {
		p.console.StopSpinner(ctx, "Failed to void deployment state", input.StepFailed)
		return fmt.Errorf("voiding deployment state: %w", err)
	}

	p.console.StopSpinner(ctx, "Deployment state voided", input.StepDone)
	return nil
}
