// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type appServiceTarget struct {
	env     *environment.Environment
	cli     *azapi.AzureClient
	console input.Console
}

// NewAppServiceTarget creates a new instance of the AppServiceTarget
func NewAppServiceTarget(
	env *environment.Environment,
	azCli *azapi.AzureClient,
	console input.Console,
) ServiceTarget {
	return &appServiceTarget{
		env:     env,
		cli:     azCli,
		console: console,
	}
}

// Gets the required external tools
func (st *appServiceTarget) RequiredExternalTools(ctx context.Context, serviceConfig *ServiceConfig) []tools.ExternalTool {
	return []tools.ExternalTool{}
}

// Initializes the AppService target
func (st *appServiceTarget) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

// Prepares a zip archive from the specified build output
func (st *appServiceTarget) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	progress.SetProgress(NewServiceProgress("Compressing deployment artifacts"))

	// Get package path from the service context
	var packagePath string
	if artifact, found := serviceContext.Package.FindFirst(WithKind(ArtifactKindDirectory)); found &&
		artifact.Location != "" {
		packagePath = artifact.Location
	}

	if packagePath == "" {
		return nil, fmt.Errorf("no package artifacts found in service context")
	}

	zipFilePath, err := createDeployableZip(
		serviceConfig,
		packagePath,
	)
	if err != nil {
		return nil, err
	}

	// Create zip artifact
	zipArtifact := &Artifact{
		Kind:         ArtifactKindArchive,
		Location:     zipFilePath,
		LocationKind: LocationKindLocal,
		Metadata: map[string]string{
			"packagePath": packagePath,
		},
	}

	return &ServicePackageResult{
		Artifacts: ArtifactCollection{zipArtifact},
	}, nil
}

func (st *appServiceTarget) Publish(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
	publishOptions *PublishOptions,
) (*ServicePublishResult, error) {
	return &ServicePublishResult{}, nil
}

// Deploys the prepared zip archive using Zip deploy to the Azure App Service resource
func (st *appServiceTarget) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	if err := st.validateTargetResource(targetResource); err != nil {
		return nil, fmt.Errorf("validating target resource: %w", err)
	}

	// Get zip file path from package artifacts
	var zipFilePath string
	if artifact, found := serviceContext.Package.FindFirst(WithKind(ArtifactKindArchive)); found && artifact.Location != "" {
		zipFilePath = artifact.Location
	}

	if zipFilePath == "" {
		return nil, fmt.Errorf("no zip artifacts found in service context")
	}

	// Determine deployment targets based on deployment history and slots
	deployTargets, err := st.determineDeploymentTargets(ctx, serviceConfig, targetResource, progress)
	if err != nil {
		return nil, fmt.Errorf("determining deployment targets: %w", err)
	}

	// Deploy to each target
	hasSlots := len(deployTargets) > 1 || (len(deployTargets) == 1 && deployTargets[0].SlotName != "")

	for _, target := range deployTargets {
		zipFile, err := os.Open(zipFilePath)
		if err != nil {
			return nil, fmt.Errorf("failed reading deployment zip file: %w", err)
		}
		defer zipFile.Close()

		var deployErr error
		if target.SlotName == "" {
			// When deploying to the main app
			var progressMsg string
			if hasSlots {
				// Mirror Azure Portal terminology: main app is the "production" slot
				progressMsg = "Uploading deployment package to production slot"
			} else {
				// No slots configured, use simple message
				progressMsg = "Uploading deployment package"
			}
			progress.SetProgress(NewServiceProgress(progressMsg))
			_, deployErr = st.cli.DeployAppServiceZip(
				ctx,
				targetResource.SubscriptionId(),
				targetResource.ResourceGroupName(),
				targetResource.ResourceName(),
				zipFile,
				func(logProgress string) { progress.SetProgress(NewServiceProgress(logProgress)) },
			)
		} else {
			progressMsg := fmt.Sprintf("Uploading deployment package to slot '%s'", target.SlotName)
			progress.SetProgress(NewServiceProgress(progressMsg))
			_, deployErr = st.cli.DeployAppServiceSlotZip(
				ctx,
				targetResource.SubscriptionId(),
				targetResource.ResourceGroupName(),
				targetResource.ResourceName(),
				target.SlotName,
				zipFile,
				func(logProgress string) { progress.SetProgress(NewServiceProgress(logProgress)) },
			)
		}

		if deployErr != nil {
			var targetName string
			if target.SlotName == "" {
				if hasSlots {
					targetName = "production slot"
				} else {
					targetName = "app service"
				}
			} else {
				targetName = fmt.Sprintf("slot '%s'", target.SlotName)
			}
			return nil, fmt.Errorf("deploying service %s to %s: %w", serviceConfig.Name, targetName, deployErr)
		}
	}

	progress.SetProgress(NewServiceProgress("Fetching endpoints for app service"))
	endpoints, err := st.Endpoints(ctx, serviceConfig, targetResource)
	if err != nil {
		return nil, err
	}

	artifacts := ArtifactCollection{}

	// Add endpoints as artifacts
	for _, endpoint := range endpoints {
		if err := artifacts.Add(&Artifact{
			Kind:         ArtifactKindEndpoint,
			Location:     endpoint,
			LocationKind: LocationKindRemote,
		}); err != nil {
			return nil, fmt.Errorf("failed to add endpoint artifact: %w", err)
		}
	}

	// Add resource artifact
	var resourceArtifact *Artifact
	if err := mapper.Convert(targetResource, &resourceArtifact); err == nil {
		if err := artifacts.Add(resourceArtifact); err != nil {
			return nil, fmt.Errorf("failed to add resource artifact: %w", err)
		}
	}

	return &ServiceDeployResult{
		Artifacts: artifacts,
	}, nil
}

// deploymentTarget represents a target for deployment (main app or a slot)
type deploymentTarget struct {
	SlotName string // Empty string means main app
}

// determineDeploymentTargets determines which targets (main app and/or slots) to deploy to
// based on deployment history and available slots.
//
// Deployment Strategy:
//   - First deployment (no history):
//     Deploy to main app AND all slots to ensure consistency across all environments.
//     This prevents configuration drift and ensures all slots start with the same baseline.
//   - Subsequent deployments with no slots:
//     Deploy to main app only (standard production deployment).
//   - Subsequent deployments with exactly one slot:
//     Deploy to that slot only (typical staging workflow before swap to production).
//   - Subsequent deployments with multiple slots:
//     Check for AZD_DEPLOY_{SERVICE_NAME}_SLOT_NAME environment variable to auto-select a slot.
//     If not set, prompt user to select a target, allowing explicit control
//     over which environment receives the deployment.
func (st *appServiceTarget) determineDeploymentTargets(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
) ([]deploymentTarget, error) {
	progress.SetProgress(NewServiceProgress("Checking deployment history"))

	// Check if there are previous deployments
	hasDeployments, err := st.cli.HasAppServiceDeployments(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
	)
	if err != nil {
		return nil, fmt.Errorf("checking deployment history: %w", err)
	}

	progress.SetProgress(NewServiceProgress("Checking deployment slots"))

	// Get available slots
	slots, err := st.cli.GetAppServiceSlots(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
	)
	if err != nil {
		return nil, fmt.Errorf("getting deployment slots: %w", err)
	}

	// If no previous deployments, always deploy to main app and all slots
	if !hasDeployments {
		targets := []deploymentTarget{{SlotName: ""}} // Main app
		for _, slot := range slots {
			targets = append(targets, deploymentTarget{SlotName: slot.Name})
		}
		return targets, nil
	}

	// Has previous deployments
	if len(slots) == 0 {
		// No slots, deploy to main app only
		return []deploymentTarget{{SlotName: ""}}, nil
	}

	if len(slots) == 1 {
		// Exactly one slot, deploy to that slot only
		return []deploymentTarget{{SlotName: slots[0].Name}}, nil
	}

	// Multiple slots, prompt user to select
	slotEnvVarName := slotEnvVarNameForService(serviceConfig.Name)

	// Check if slot name is set via environment variable (checks azd env first, then system env)
	if slotName := st.env.Getenv(slotEnvVarName); slotName != "" {
		// Validate that the slot exists
		for _, slot := range slots {
			if slot.Name == slotName {
				return []deploymentTarget{{SlotName: slotName}}, nil
			}
		}
		// Slot not found, return error with available slots
		availableSlots := make([]string, len(slots))
		for i, slot := range slots {
			availableSlots[i] = slot.Name
		}
		return nil, fmt.Errorf(
			"slot '%s' specified in %s not found. Available slots: [%s]. "+
				"Please update the environment variable with a valid slot name",
			slotName, slotEnvVarName, strings.Join(availableSlots, ", "))
	}

	slotOptions := make([]string, len(slots))
	for i, slot := range slots {
		slotOptions[i] = slot.Name
	}

	selectedIndex, err := st.console.Select(ctx, input.ConsoleOptions{
		Message: fmt.Sprintf(
			"Select a deployment slot\nNote: skip this prompt with '%s=<slotName>'\n",
			slotEnvVarName),
		Options: slotOptions,
	})
	if err != nil {
		return nil, fmt.Errorf("selecting deployment slot: %w", err)
	}

	return []deploymentTarget{{SlotName: slots[selectedIndex].Name}}, nil
}

// slotEnvVarNameForService returns the environment variable name for setting the deployment slot
// for a given service. The format is AZD_DEPLOY_{SERVICE_NAME}_SLOT_NAME where the service name
// is uppercase and any hyphens are replaced with underscores.
func slotEnvVarNameForService(serviceName string) string {
	normalizedName := strings.ToUpper(strings.ReplaceAll(serviceName, "-", "_"))
	return fmt.Sprintf("AZD_DEPLOY_%s_SLOT_NAME", normalizedName)
}

// Gets the exposed endpoints for the App Service, including any deployment slots
func (st *appServiceTarget) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	// Get main app properties
	appServiceProperties, err := st.cli.GetAppServiceProperties(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
	)
	if err != nil {
		return nil, fmt.Errorf("fetching service properties: %w", err)
	}

	var endpoints []string

	// Add main app endpoints
	for _, hostName := range appServiceProperties.HostNames {
		endpoints = append(endpoints, fmt.Sprintf("https://%s/", hostName))
	}

	// Get all deployment slots
	slots, err := st.cli.GetAppServiceSlots(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
	)
	if err != nil {
		// Log but don't fail if we can't get slots - main app endpoints are still valid
		return endpoints, nil
	}

	// Add slot endpoints with prefix
	for _, slot := range slots {
		slotProperties, err := st.cli.GetAppServiceSlotProperties(
			ctx,
			targetResource.SubscriptionId(),
			targetResource.ResourceGroupName(),
			targetResource.ResourceName(),
			slot.Name,
		)
		if err != nil {
			// Skip this slot if we can't get its properties
			continue
		}

		for _, hostName := range slotProperties.HostNames {
			endpoints = append(endpoints, fmt.Sprintf("https://%s/", hostName))
		}
	}

	return endpoints, nil
}

func (st *appServiceTarget) validateTargetResource(
	targetResource *environment.TargetResource,
) error {
	if !strings.EqualFold(targetResource.ResourceType(), string(azapi.AzureResourceTypeWebSite)) {
		return resourceTypeMismatchError(
			targetResource.ResourceName(),
			targetResource.ResourceType(),
			azapi.AzureResourceTypeWebSite,
		)
	}

	return nil
}

// Tasks returns the list of available tasks for this service target.
func (st *appServiceTarget) Tasks(ctx context.Context, serviceConfig *ServiceConfig) []ServiceTask {
	return []ServiceTask{
		{Name: "swap"},
	}
}

// Task executes a specific task for this service target.
func (st *appServiceTarget) Task(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
	task ServiceTask,
	taskArgs string,
) error {
	if task.Name == "swap" {
		return st.handleSwapTask(ctx, serviceConfig, targetResource, taskArgs)
	}

	return fmt.Errorf("task '%s' is not supported", task.Name)
}

// handleSwapTask handles the swap task for app service deployment slots
func (st *appServiceTarget) handleSwapTask(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
	taskArgs string,
) error {
	// Parse task arguments to get src and dst
	srcSlot, dstSlot := parseTaskArgs(taskArgs)

	// Get the list of deployment slots
	slots, err := st.cli.GetAppServiceSlots(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
	)
	if err != nil {
		return fmt.Errorf("getting deployment slots: %w", err)
	}

	// Check if there are any slots
	if len(slots) == 0 {
		return fmt.Errorf("swap operation requires a service with at least one slot")
	}

	// Build the list of all slot names (including production as empty string)
	slotNames := []string{""} // Production is represented as empty string
	for _, slot := range slots {
		slotNames = append(slotNames, slot.Name)
	}

	// If there's only one slot, auto-select based on the scenario
	if len(slots) == 1 {
		onlySlot := slots[0].Name
		if srcSlot == "" && dstSlot == "" {
			// No task arguments provided - default behavior: swap slot to production
			srcSlot = onlySlot
			dstSlot = ""
		} else {
			// Task arguments provided - validate they match the only slot and production
			if !isValidSlotName(srcSlot, slotNames) || !isValidSlotName(dstSlot, slotNames) {
				return fmt.Errorf("invalid slot name in task arguments")
			}
			// Ensure at least one is the only slot
			if srcSlot != onlySlot && dstSlot != onlySlot {
				return fmt.Errorf("at least one slot must be '%s' when there is only one slot", onlySlot)
			}
		}
	} else {
		// Multiple slots - prompt if arguments not provided
		if srcSlot == "" || dstSlot == "" {
			// Prompt for source slot
			if srcSlot == "" {
				srcOptions := []string{"@main (production)"}
				for _, slot := range slots {
					srcOptions = append(srcOptions, slot.Name)
				}

				srcIndex, err := st.console.Select(ctx, input.ConsoleOptions{
					Message: "Select the source slot:",
					Options: srcOptions,
				})
				if err != nil {
					return fmt.Errorf("selecting source slot: %w", err)
				}

				if srcIndex == 0 {
					srcSlot = "" // @main (production)
				} else {
					srcSlot = slots[srcIndex-1].Name
				}
			}

			// Prompt for destination slot (excluding the selected source)
			if dstSlot == "" {
				dstOptions := []string{}
				if srcSlot != "" {
					dstOptions = append(dstOptions, "@main (production)")
				}
				for _, slot := range slots {
					if slot.Name != srcSlot {
						dstOptions = append(dstOptions, slot.Name)
					}
				}

				dstIndex, err := st.console.Select(ctx, input.ConsoleOptions{
					Message: "Select the destination slot:",
					Options: dstOptions,
				})
				if err != nil {
					return fmt.Errorf("selecting destination slot: %w", err)
				}

				// Map selected index back to slot name
				dstSlot = dstOptions[dstIndex]
				if dstSlot == "@main (production)" {
					dstSlot = ""
				}
			}
		}

		// Validate slot names
		if !isValidSlotName(srcSlot, slotNames) {
			return fmt.Errorf("invalid source slot: %s", srcSlot)
		}
		if !isValidSlotName(dstSlot, slotNames) {
			return fmt.Errorf("invalid destination slot: %s", dstSlot)
		}
	}

	// Validate that source and destination are different
	if srcSlot == dstSlot {
		return fmt.Errorf("source and destination slots cannot be the same")
	}

	// Get display names for confirmation
	srcDisplay := srcSlot
	if srcDisplay == "" {
		srcDisplay = "@main (production)"
	}
	dstDisplay := dstSlot
	if dstDisplay == "" {
		dstDisplay = "@main (production)"
	}

	// Confirm the swap
	confirmed, err := st.console.Confirm(ctx, input.ConsoleOptions{
		Message:      fmt.Sprintf("Swap '%s' with '%s'?", srcDisplay, dstDisplay),
		DefaultValue: true,
	})
	if err != nil {
		return fmt.Errorf("confirming swap: %w", err)
	}

	if !confirmed {
		return fmt.Errorf("swap cancelled by user")
	}

	// Perform the swap
	st.console.Message(ctx, fmt.Sprintf("Swapping '%s' with '%s'...", srcDisplay, dstDisplay))

	err = st.cli.SwapSlot(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		targetResource.ResourceName(),
		srcSlot,
		dstSlot,
	)
	if err != nil {
		return fmt.Errorf("swapping slots: %w", err)
	}

	st.console.Message(ctx, "Swap completed successfully.")
	return nil
}

// parseTaskArgs parses task arguments in the format "key=value;key2=value2"
// Returns (sourceSlot, destinationSlot) as strings
// The value "@main" is normalized to an empty string to represent the main app (production slot).
func parseTaskArgs(taskArgs string) (string, string) {
	if taskArgs == "" {
		return "", ""
	}

	var sourceSlot, destinationSlot string
	parts := strings.Split(taskArgs, ";")
	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		value := strings.TrimSpace(kv[1])

		// Normalize "@main" to empty string (internal representation for main app/production slot)
		if strings.EqualFold(value, "@main") {
			value = ""
		}

		switch key {
		case "src":
			sourceSlot = value
		case "dst":
			destinationSlot = value
		}
	}

	return sourceSlot, destinationSlot
}

// isValidSlotName checks if a slot name is valid (exists in the list of available slots)
func isValidSlotName(name string, availableSlots []string) bool {
	for _, slot := range availableSlots {
		if slot == name {
			return true
		}
	}
	return false
}
