// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armlocks"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
)

// errUserCancelled is returned when the user declines the resource group deletion confirmation.
// The caller uses this to distinguish user cancellation from successful completion.
var errUserCancelled = errors.New("user cancelled resource group deletion")

// forceDeleteLogAnalyticsIfPurge force-deletes Log Analytics Workspaces in the given resource
// groups when purge is enabled. This must happen while the workspaces still exist — force-delete
// is not possible after the containing resource group is deleted.
func (p *BicepProvider) forceDeleteLogAnalyticsIfPurge(
	ctx context.Context,
	resources map[string][]*azapi.Resource,
	options provisioning.DestroyOptions,
) error {
	if !options.Purge() {
		return nil
	}
	workspaces, err := p.getLogAnalyticsWorkspacesToPurge(ctx, resources)
	if err != nil {
		return fmt.Errorf("getting log analytics workspaces to purge: %w", err)
	}
	if len(workspaces) > 0 {
		if err := p.forceDeleteLogAnalyticsWorkspaces(ctx, workspaces); err != nil {
			return fmt.Errorf(
				"force deleting log analytics workspaces: %w", err,
			)
		}
	}
	return nil
}

// classifyResourceGroups classifies each resource group as owned/external/unknown
// using the 4-tier pipeline. Returns owned RG names and skipped RGs.
//
// When a Bicep snapshot is available (bicepparam mode), snapshot-based classification
// is used as the primary mechanism: RGs in predictedResources are owned, others are external.
// This replaces Tiers 1-3 with a deterministic, offline signal. Tier 4 still runs on owned
// candidates as defense-in-depth.
//
// When snapshot is unavailable (non-bicepparam mode, older Bicep CLI, or snapshot error),
// the full Tier 1-4 pipeline runs as fallback.
//
// When force is true, only Tier 1 (zero extra API calls) runs. External RGs identified
// by deployment operations (Read/EvaluateDeploymentOutput) are still protected. Unknown
// RGs (no operation data) are treated as owned. This provides free safety while preserving
// --force semantics (no prompts, no extra API calls). If operations are unavailable,
// all RGs are returned as owned for backward compatibility.
//
// This function does NOT delete any resource groups — the caller is responsible
// for deletion after collecting purge targets (which require the RGs to still exist).
//
// Log Analytics Workspaces in owned RGs are force-deleted before the RG if purge is enabled,
// since force-delete requires the workspace to still exist.
// Returns the list of owned RG names and any skipped RG info.
func (p *BicepProvider) classifyResourceGroups(
	ctx context.Context,
	deployment infra.Deployment,
	groupedResources map[string][]*azapi.Resource,
	options provisioning.DestroyOptions,
) (owned []string, skipped []azapi.ClassifiedSkip, err error) {
	// Extract RG names from the grouped resources map.
	rgNames := slices.Collect(maps.Keys(groupedResources))

	// Get deployment info for classification (used for logging and hash derivation).
	deploymentInfo, deployInfoErr := deployment.Get(ctx)
	if deployInfoErr == nil {
		log.Printf("classifying resource groups for deployment: %s", deploymentInfo.Name)
	}

	// Get deployment operations (Tier 1 data — single API call).
	// Fetched even with --force: Tier 1 is free and protects external RGs.
	var operations []*armresources.DeploymentOperation
	operations, err = deployment.Operations(ctx)
	if err != nil {
		if options.Force() {
			// --force with unavailable operations: delete all (backward compat).
			log.Printf(
				"WARNING: --force with unavailable deployment operations — all %d RGs will be deleted.",
				len(rgNames),
			)
			return rgNames, nil, nil
		}
		// Normal mode: operations unavailable — classification will fall to Tier 2/3.
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
		ForceMode:                  options.Force(),
		EnvName:                    p.env.Name(),
		ExpectedProvisionParamHash: expectedHash,
		SnapshotPredictedRGs:       p.getSnapshotPredictedRGs(ctx),
	}

	// Only wire Tier 2/3/4 callbacks when not --force (they won't be invoked in ForceMode).
	if !options.Force() {
		classifyOpts.GetResourceGroupTags = func(ctx context.Context, rgName string) (map[string]*string, error) {
			return p.getResourceGroupTags(ctx, subscriptionId, rgName)
		}
		classifyOpts.ListResourceGroupLocks = func(ctx context.Context, rgName string) ([]*azapi.ManagementLock, error) {
			return p.listResourceGroupLocks(ctx, subscriptionId, rgName)
		}
		classifyOpts.ListResourceGroupResources = func(
			ctx context.Context, rgName string,
		) ([]*azapi.ResourceWithTags, error) {
			return p.listResourceGroupResourcesWithTags(ctx, subscriptionId, rgName)
		}
		classifyOpts.Prompter = func(rgName, reason string) (bool, error) {
			return p.console.Confirm(ctx, input.ConsoleOptions{
				Message:      fmt.Sprintf("Delete resource group '%s'? (%s)", rgName, reason),
				DefaultValue: false,
			})
		}
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
			return nil, result.Skipped, errUserCancelled
		}
	}

	return result.Owned, result.Skipped, nil
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
		if laErr := p.forceDeleteLogAnalyticsIfPurge(ctx, rgResources, options); laErr != nil {
			deleteErrors = append(deleteErrors,
				fmt.Errorf("log analytics purge for %s: %w", rgName, laErr))
			continue
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
// which causes the classifier to fall to Tier 3 (more scrutiny — safe direction).
// This differs from listResourceGroupLocks/listResourceGroupResourcesWithTags which
// return errors → fail-safe veto. The asymmetry is intentional: missing tags means
// "try harder to verify," while missing lock/resource data means "don't delete."
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

	// Tags are included by default in GenericResourceExpanded — no $expand needed.
	var resources []*azapi.ResourceWithTags
	pager := client.NewListByResourceGroupPager(rgName, nil)
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
			resType := ""
			if res.Type != nil {
				resType = *res.Type
			}
			resources = append(resources, &azapi.ResourceWithTags{
				Name: name,
				Type: resType,
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
// the standard classification-based path (classifyResourceGroups + deleteRGList).
func (p *BicepProvider) isDeploymentStacksEnabled() bool {
	var featureManager *alpha.FeatureManager
	if err := p.serviceLocator.Resolve(&featureManager); err != nil {
		return false
	}
	return featureManager.IsEnabled(azapi.FeatureDeploymentStacks)
}

// getSnapshotPredictedRGs invokes `bicep snapshot` on the current template and extracts
// the set of resource group names from predictedResources. Returns a map of lowercased
// RG names (for case-insensitive lookup), or nil if snapshot is unavailable.
//
// Snapshot is only available in bicepparam mode (the modern default) because the Bicep CLI
// requires a .bicepparam file as input. In non-bicepparam mode with available parameters,
// a temporary .bicepparam file is generated.
//
// On any error (older Bicep CLI, compilation failure, etc.), logs a warning and returns nil,
// which causes the classifier to fall back to the Tier 1-4 pipeline.
func (p *BicepProvider) getSnapshotPredictedRGs(ctx context.Context) map[string]bool {
	compileResult := p.compileBicepMemoryCache
	if compileResult == nil {
		log.Printf("snapshot classification: compileBicep cache unavailable, skipping snapshot")
		return nil
	}

	// Determine the .bicepparam file to use for the snapshot.
	var bicepParamFile string
	var cleanupFn func()

	if p.mode == bicepparamMode {
		// In bicepparam mode, p.path IS the .bicepparam file — use it directly.
		bicepParamFile = p.path
	} else if len(compileResult.Parameters) > 0 {
		// Non-bicepparam mode with available parameters: generate a temp .bicepparam file.
		bicepFileName := filepath.Base(p.path)
		moduleDir := filepath.Dir(p.path)

		bicepParamContent := generateBicepParam(bicepFileName, compileResult.Parameters)

		tmpFile, err := os.CreateTemp(moduleDir, "snapshot-*.bicepparam")
		if err != nil {
			log.Printf("snapshot classification: failed to create temp bicepparam: %v", err)
			return nil
		}
		bicepParamFile = tmpFile.Name()
		cleanupFn = func() {
			tmpFile.Close()
			os.Remove(bicepParamFile)
		}

		if _, err := tmpFile.WriteString(bicepParamContent); err != nil {
			cleanupFn()
			log.Printf("snapshot classification: failed to write temp bicepparam: %v", err)
			return nil
		}
		if err := tmpFile.Close(); err != nil {
			cleanupFn()
			log.Printf("snapshot classification: failed to close temp bicepparam: %v", err)
			return nil
		}
	} else {
		// Non-bicepparam mode without parameters: cannot generate .bicepparam for snapshot.
		log.Printf("snapshot classification: non-bicepparam mode without parameters, skipping snapshot")
		return nil
	}
	if cleanupFn != nil {
		defer cleanupFn()
	}

	// Build snapshot options from environment.
	snapshotOpts := bicep.NewSnapshotOptions().
		WithSubscriptionID(p.env.GetSubscriptionId())

	if loc := p.env.GetLocation(); loc != "" {
		snapshotOpts = snapshotOpts.WithLocation(loc)
	}
	if rg := p.env.Getenv(environment.ResourceGroupEnvVarName); rg != "" {
		snapshotOpts = snapshotOpts.WithResourceGroup(rg)
	}

	// Run the Bicep snapshot command.
	data, err := p.bicepCli.Snapshot(ctx, bicepParamFile, snapshotOpts)
	if err != nil {
		log.Printf("snapshot classification: bicep snapshot unavailable: %v", err)
		return nil
	}

	// Parse and extract resource group names.
	var snapshot snapshotResult
	if err := json.Unmarshal(data, &snapshot); err != nil {
		log.Printf("snapshot classification: failed to parse snapshot: %v", err)
		return nil
	}

	predictedRGs := make(map[string]bool)
	for _, res := range snapshot.PredictedResources {
		if strings.EqualFold(res.Type, "Microsoft.Resources/resourceGroups") && res.Name != "" {
			predictedRGs[strings.ToLower(res.Name)] = true
		}
	}

	if len(predictedRGs) == 0 {
		// No RGs in predictedResources — could mean a resource-group-scoped deployment
		// where RGs aren't declared as resources. Fall back to tier system.
		log.Printf("snapshot classification: no resource groups found in predictedResources, falling back to tiers")
		return nil
	}

	log.Printf("snapshot classification: found %d predicted resource group(s): %v",
		len(predictedRGs), slices.Collect(maps.Keys(predictedRGs)))
	return predictedRGs
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
	if err := p.forceDeleteLogAnalyticsIfPurge(ctx, groupedResources, options); err != nil {
		return fmt.Errorf("log analytics purge before deployment delete: %w", err)
	}

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
