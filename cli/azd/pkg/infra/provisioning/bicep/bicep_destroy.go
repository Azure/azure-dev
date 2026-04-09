// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armlocks"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// forceDeleteLogAnalyticsIfPurge force-deletes Log Analytics Workspaces in the given resource
// groups when purge is enabled. This must happen while the workspaces still exist — force-delete
// is not possible after the containing resource group is deleted.
func (p *BicepProvider) forceDeleteLogAnalyticsIfPurge(
	ctx context.Context,
	resources map[string][]*azapi.Resource,
	options provisioning.DestroyOptions,
) {
	if !options.Purge() {
		return
	}
	workspaces, err := p.getLogAnalyticsWorkspacesToPurge(ctx, resources)
	if err != nil {
		log.Printf("WARNING: could not list log analytics workspaces: %v", err)
		return
	}
	if len(workspaces) > 0 {
		if err := p.forceDeleteLogAnalyticsWorkspaces(ctx, workspaces); err != nil {
			log.Printf("WARNING: force-deleting log analytics workspaces: %v", err)
		}
	}
}

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

	// Derive expected provision param hash from deployment tags for Tier 2 verification.
	var expectedHash string
	if deployInfoErr == nil && deploymentInfo.Tags != nil {
		if h := deploymentInfo.Tags[azapi.TagKeyProvisionParamHash]; h != nil {
			expectedHash = *h
		}
	}

	// Build classification options.
	subscriptionId := deployment.SubscriptionId()
	classifyOpts := azapi.ClassifyOptions{
		Interactive:                !p.console.IsNoPromptMode(),
		EnvName:                    p.env.Name(),
		ExpectedProvisionParamHash: expectedHash,
		GetResourceGroupTags: func(ctx context.Context, rgName string) (map[string]*string, error) {
			return p.getResourceGroupTags(ctx, subscriptionId, rgName)
		},
		ListResourceGroupLocks: func(ctx context.Context, rgName string) ([]*azapi.ManagementLock, error) {
			return p.listResourceGroupLocks(ctx, subscriptionId, rgName)
		},
		ListResourceGroupResources: func(
			ctx context.Context, rgName string,
		) ([]*azapi.ResourceWithTags, error) {
			return p.listResourceGroupResourcesWithTags(ctx, subscriptionId, rgName)
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

	// Overall confirmation prompt for owned RGs (interactive only, not --force).
	if len(result.Owned) > 0 && !options.Force() && !p.console.IsNoPromptMode() {
		confirmMsg := fmt.Sprintf(
			"Delete %d resource group(s): %s?",
			len(result.Owned),
			strings.Join(result.Owned, ", "),
		)
		confirmed, confirmErr := p.console.Confirm(ctx, input.ConsoleOptions{
			Message:      confirmMsg,
			DefaultValue: false,
		})
		if confirmErr != nil {
			return nil, result.Skipped, fmt.Errorf("confirming resource group deletion: %w", confirmErr)
		}
		if !confirmed {
			return nil, result.Skipped, nil
		}
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
		rgResources := map[string][]*azapi.Resource{rgName: groupedResources[rgName]}
		p.forceDeleteLogAnalyticsIfPurge(ctx, rgResources, options)

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

// listResourceGroupLocks retrieves management locks on a resource group using the ARM API.
// Returns an error if dependencies cannot be resolved — the classifier treats
// errors as vetoes (fail-safe) to avoid deleting locked resources without verification.
func (p *BicepProvider) listResourceGroupLocks(
	ctx context.Context,
	subscriptionId string,
	rgName string,
) ([]*azapi.ManagementLock, error) {
	var credProvider account.SubscriptionCredentialProvider
	if err := p.serviceLocator.Resolve(&credProvider); err != nil {
		return nil, fmt.Errorf(
			"classify locks: credential provider unavailable for rg=%s: %w",
			rgName, err,
		)
	}

	var armOpts *arm.ClientOptions
	_ = p.serviceLocator.Resolve(&armOpts) // optional; nil is a valid default

	credential, err := credProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf(
			"classify locks: credential error for rg=%s: %w", rgName, err,
		)
	}

	client, err := armlocks.NewManagementLocksClient(subscriptionId, credential, armOpts)
	if err != nil {
		return nil, fmt.Errorf(
			"classify locks: ARM client error for rg=%s: %w", rgName, err,
		)
	}

	var locks []*azapi.ManagementLock
	pager := client.NewListAtResourceGroupLevelPager(rgName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err // propagate so caller can handle 404/403
		}
		for _, lock := range page.Value {
			if lock == nil || lock.Properties == nil {
				continue
			}
			name := ""
			if lock.Name != nil {
				name = *lock.Name
			}
			lockType := ""
			if lock.Properties.Level != nil {
				lockType = string(*lock.Properties.Level)
			}
			ml := &azapi.ManagementLock{Name: name, LockType: lockType}
			locks = append(locks, ml)
			// Short-circuit: one blocking lock is enough to veto.
			if strings.EqualFold(lockType, azapi.LockLevelCanNotDelete) ||
				strings.EqualFold(lockType, azapi.LockLevelReadOnly) {
				return locks, nil
			}
		}
	}
	return locks, nil
}

// listResourceGroupResourcesWithTags retrieves all resources in a resource group
// with their tags, used for Tier 4 foreign-resource detection.
// Returns an error if dependencies cannot be resolved — the classifier treats
// errors as vetoes (fail-safe) to avoid deleting resources without verification.
func (p *BicepProvider) listResourceGroupResourcesWithTags(
	ctx context.Context,
	subscriptionId string,
	rgName string,
) ([]*azapi.ResourceWithTags, error) {
	var credProvider account.SubscriptionCredentialProvider
	if err := p.serviceLocator.Resolve(&credProvider); err != nil {
		return nil, fmt.Errorf(
			"classify resources: credential provider unavailable for rg=%s: %w",
			rgName, err,
		)
	}

	var armOpts *arm.ClientOptions
	_ = p.serviceLocator.Resolve(&armOpts) // optional; nil is a valid default

	credential, err := credProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf(
			"classify resources: credential error for rg=%s: %w", rgName, err,
		)
	}

	client, err := armresources.NewClient(subscriptionId, credential, armOpts)
	if err != nil {
		return nil, fmt.Errorf(
			"classify resources: ARM client error for rg=%s: %w", rgName, err,
		)
	}

	// Use $expand=tags to include resource tags in the response.
	expand := "tags"
	var resources []*azapi.ResourceWithTags
	pager := client.NewListByResourceGroupPager(
		rgName,
		&armresources.ClientListByResourceGroupOptions{Expand: &expand},
	)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err // propagate so caller can handle 404/403
		}
		for _, res := range page.Value {
			if res == nil {
				continue
			}
			name := ""
			if res.Name != nil {
				name = *res.Name
			}
			resources = append(resources, &azapi.ResourceWithTags{
				Name: name,
				Tags: res.Tags,
			})
		}
	}
	return resources, nil
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

// isDeploymentStacksEnabled checks if the deployment stacks alpha feature is enabled.
// Used to determine whether to use the stack-based delete path (deployment.Delete) or
// the standard classification-based path (classifyAndDeleteResourceGroups).
func (p *BicepProvider) isDeploymentStacksEnabled() bool {
	var featureManager *alpha.FeatureManager
	if err := p.serviceLocator.Resolve(&featureManager); err != nil {
		return false
	}
	return featureManager.IsEnabled(azapi.FeatureDeploymentStacks)
}

// destroyViaDeploymentDelete deletes resources using deployment.Delete(), which routes
// through the deployment service (standard or stacks). For deployment stacks, this deletes
// the stack object which cascades to managed resources. This path does NOT perform
// resource group classification — it is the pre-existing behavior preserved for
// deployment stacks where the stack manages resource lifecycle.
func (p *BicepProvider) destroyViaDeploymentDelete(
	ctx context.Context,
	deployment infra.Deployment,
	groupedResources map[string][]*azapi.Resource,
	options provisioning.DestroyOptions,
) error {
	// Force-delete Log Analytics Workspaces before deleting the deployment/stack,
	// since force-delete requires the workspace to still exist.
	p.forceDeleteLogAnalyticsIfPurge(ctx, groupedResources, options)

	// Delete via the deployment service (standard: deletes RGs; stacks: deletes the stack).
	err := async.RunWithProgressE(func(progressMessage azapi.DeleteDeploymentProgress) {
		switch progressMessage.State {
		case azapi.DeleteResourceStateInProgress:
			p.console.ShowSpinner(ctx, progressMessage.Message, input.Step)
		case azapi.DeleteResourceStateSucceeded:
			p.console.StopSpinner(ctx, progressMessage.Message, input.StepDone)
		case azapi.DeleteResourceStateFailed:
			p.console.StopSpinner(ctx, progressMessage.Message, input.StepFailed)
		}
	}, func(progress *async.Progress[azapi.DeleteDeploymentProgress]) error {
		optionsMap, err := convert.ToMap(p.options)
		if err != nil {
			return err
		}
		return deployment.Delete(ctx, optionsMap, progress)
	})

	if err != nil {
		return err
	}

	p.console.Message(ctx, "")
	return nil
}
