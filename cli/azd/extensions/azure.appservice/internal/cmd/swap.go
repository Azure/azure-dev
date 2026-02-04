// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

type swapFlags struct {
	service string
	src     string
	dst     string
}

func newSwapCommand(rootFlags rootFlagsDefinition) *cobra.Command {
	flags := &swapFlags{}

	cmd := &cobra.Command{
		Use:   "swap",
		Short: "Swap deployment slots for an App Service.",
		Long: `Swap deployment slots for an Azure App Service.

This command allows you to swap the content between two deployment slots,
or between a slot and the production environment.

Use @main to refer to the production slot.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSwap(cmd.Context(), flags, rootFlags)
		},
	}

	cmd.Flags().StringVar(&flags.service, "service", "", "The name of the service to swap slots for.")
	cmd.Flags().StringVar(&flags.src, "src", "", "The source slot name. Use @main for production.")
	cmd.Flags().StringVar(&flags.dst, "dst", "", "The destination slot name. Use @main for production.")

	return cmd
}

func runSwap(ctx context.Context, flags *swapFlags, rootFlags rootFlagsDefinition) error {
	// Create a new context that includes the AZD access token
	ctx = azdext.WithAccessToken(ctx)

	// Create a new AZD client
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return fmt.Errorf("failed to create azd client: %w", err)
	}
	defer azdClient.Close()

	// Wait for debugger if AZD_EXT_DEBUG is set
	if err := azdext.WaitForDebugger(ctx, azdClient); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, azdext.ErrDebuggerAborted) {
			return nil
		}
		return fmt.Errorf("failed waiting for debugger: %w", err)
	}

	// Get the deployment context from azd
	deploymentContext, err := azdClient.Deployment().GetDeploymentContext(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return fmt.Errorf("failed to get deployment context: %w", err)
	}

	subscriptionId := deploymentContext.AzureContext.Scope.SubscriptionId
	if subscriptionId == "" {
		return fmt.Errorf("no subscription ID found in deployment context")
	}

	// Get the resolved services from azd
	servicesResponse, err := azdClient.Project().GetResolvedServices(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return fmt.Errorf("failed to get services: %w", err)
	}

	// Filter to only appservice services
	var appserviceServices []*azdext.ServiceConfig
	for _, svc := range servicesResponse.Services {
		if svc.Host == "appservice" {
			appserviceServices = append(appserviceServices, svc)
		}
	}

	if len(appserviceServices) == 0 {
		return fmt.Errorf("no App Service services found in the project")
	}

	// Determine which service to use
	var selectedService *azdext.ServiceConfig
	if flags.service != "" {
		// Service specified via flag
		for _, svc := range appserviceServices {
			if svc.Name == flags.service {
				selectedService = svc
				break
			}
		}
		if selectedService == nil {
			return fmt.Errorf("service '%s' not found or is not an App Service", flags.service)
		}
	} else if len(appserviceServices) == 1 {
		// Only one App Service, use it
		selectedService = appserviceServices[0]
	} else {
		// Multiple services - prompt user to select
		choices := make([]*azdext.SelectChoice, len(appserviceServices))
		for i, svc := range appserviceServices {
			choices[i] = &azdext.SelectChoice{
				Value: svc.Name,
				Label: svc.Name,
			}
		}
		prompt, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message: "Select a service:",
				Choices: choices,
			},
		})
		if err != nil {
			return fmt.Errorf("selecting service: %w", err)
		}
		selectedService = appserviceServices[prompt.GetValue()]
	}

	color.Cyan("Using service: %s", selectedService.Name)

	// Find the App Service resource for this service
	var appServiceResourceId string
	for _, resourceId := range deploymentContext.AzureContext.Resources {
		resource, err := arm.ParseResourceID(resourceId)
		if err != nil {
			continue
		}
		// Check if this is a web app resource
		if strings.EqualFold(resource.ResourceType.Type, "sites") &&
			strings.EqualFold(resource.ResourceType.Namespace, "Microsoft.Web") {
			// Check if the resource name matches the service name or resource name from config
			if strings.EqualFold(resource.Name, selectedService.Name) ||
				(selectedService.ResourceName != "" && strings.EqualFold(resource.Name, selectedService.ResourceName)) {
				appServiceResourceId = resourceId
				break
			}
		}
	}

	if appServiceResourceId == "" {
		return fmt.Errorf("could not find App Service resource for service '%s'", selectedService.Name)
	}

	// Parse the resource ID
	resource, err := arm.ParseResourceID(appServiceResourceId)
	if err != nil {
		return fmt.Errorf("failed to parse resource ID: %w", err)
	}

	resourceGroup := resource.ResourceGroupName
	appName := resource.Name

	// Create Azure credential using AzureDeveloperCLICredential
	credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID: deploymentContext.AzureContext.Scope.TenantId,
	})
	if err != nil {
		return fmt.Errorf("failed to create credential: %w", err)
	}

	// Create the web apps client
	client, err := armappservice.NewWebAppsClient(subscriptionId, credential, nil)
	if err != nil {
		return fmt.Errorf("failed to create web apps client: %w", err)
	}

	// Get the list of deployment slots
	slots, err := getAppServiceSlots(ctx, client, resourceGroup, appName)
	if err != nil {
		return fmt.Errorf("getting deployment slots: %w", err)
	}

	// Check if there are any slots
	if len(slots) == 0 {
		return fmt.Errorf("swap operation requires a service with at least one deployment slot")
	}

	// Normalize src and dst flags
	srcSlot := normalizeSlotName(flags.src)
	dstSlot := normalizeSlotName(flags.dst)
	srcProvided := flags.src != ""
	dstProvided := flags.dst != ""

	// Build the list of all slot names (including production as empty string)
	slotNames := []string{""} // Production is represented as empty string
	for _, slot := range slots {
		slotNames = append(slotNames, slot)
	}

	// If there's only one slot, auto-select based on the scenario
	if len(slots) == 1 {
		onlySlot := slots[0]
		if !srcProvided && !dstProvided {
			// No arguments provided - default behavior: swap slot to production
			srcSlot = onlySlot
			dstSlot = ""
		} else {
			// Arguments provided - validate they match the only slot and production
			if !isValidSlotName(srcSlot, slotNames) || !isValidSlotName(dstSlot, slotNames) {
				return fmt.Errorf("invalid slot name")
			}
			// Ensure at least one is the only slot
			if srcSlot != onlySlot && dstSlot != onlySlot {
				return fmt.Errorf("at least one slot must be '%s' when there is only one slot", onlySlot)
			}
		}
	} else {
		// Multiple slots - prompt if arguments not provided
		if !srcProvided || !dstProvided {
			// Prompt for source slot
			if !srcProvided {
				srcChoices := []*azdext.SelectChoice{{Value: "", Label: "@main (production)"}}
				for _, slot := range slots {
					srcChoices = append(srcChoices, &azdext.SelectChoice{Value: slot, Label: slot})
				}

				prompt, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
					Options: &azdext.SelectOptions{
						Message: "Select the source slot:",
						Choices: srcChoices,
					},
				})
				if err != nil {
					return fmt.Errorf("selecting source slot: %w", err)
				}

				srcSlot = srcChoices[prompt.GetValue()].Value
			}

			// Prompt for destination slot (excluding the selected source)
			if !dstProvided {
				dstChoices := []*azdext.SelectChoice{}
				if srcSlot != "" {
					dstChoices = append(dstChoices, &azdext.SelectChoice{Value: "", Label: "@main (production)"})
				}
				for _, slot := range slots {
					if slot != srcSlot {
						dstChoices = append(dstChoices, &azdext.SelectChoice{Value: slot, Label: slot})
					}
				}

				prompt, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
					Options: &azdext.SelectOptions{
						Message: "Select the destination slot:",
						Choices: dstChoices,
					},
				})
				if err != nil {
					return fmt.Errorf("selecting destination slot: %w", err)
				}

				dstSlot = dstChoices[prompt.GetValue()].Value
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

	// Confirm the swap unless --no-prompt is set
	if !rootFlags.NoPrompt {
		defaultValue := true
		confirmPrompt, err := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
			Options: &azdext.ConfirmOptions{
				Message:      fmt.Sprintf("Swap '%s' with '%s'?", srcDisplay, dstDisplay),
				DefaultValue: &defaultValue,
			},
		})
		if err != nil {
			return fmt.Errorf("confirming swap: %w", err)
		}

		if confirmPrompt.Value == nil || !*confirmPrompt.Value {
			color.Yellow("Swap cancelled by user.")
			return nil
		}
	}

	// Perform the swap
	color.Cyan("Swapping '%s' with '%s'...", srcDisplay, dstDisplay)

	err = swapSlot(ctx, client, resourceGroup, appName, srcSlot, dstSlot)
	if err != nil {
		return fmt.Errorf("swapping slots: %w", err)
	}

	color.Green("Swap completed successfully.")
	return nil
}

func normalizeSlotName(slot string) string {
	// Normalize "@main" to empty string (internal representation for main app/production slot)
	if strings.EqualFold(slot, "@main") {
		return ""
	}
	return slot
}

func isValidSlotName(name string, availableSlots []string) bool {
	for _, slot := range availableSlots {
		if slot == name {
			return true
		}
	}
	return false
}

func getAppServiceSlots(
	ctx context.Context,
	client *armappservice.WebAppsClient,
	resourceGroup string,
	appName string,
) ([]string, error) {
	var slots []string
	pager := client.NewListSlotsPager(resourceGroup, appName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing webapp slots: %w", err)
		}
		for _, slot := range page.Value {
			if slot.Name != nil {
				// Slot names are returned as "appName/slotName", extract just the slot name
				slotName := *slot.Name
				if idx := strings.LastIndex(slotName, "/"); idx != -1 {
					slotName = slotName[idx+1:]
				}
				slots = append(slots, slotName)
			}
		}
	}

	return slots, nil
}

func swapSlot(
	ctx context.Context,
	client *armappservice.WebAppsClient,
	resourceGroup string,
	appName string,
	sourceSlot string,
	targetSlot string,
) error {
	// Handle the swap based on which slots are involved
	var poller interface{}
	var swapErr error

	if sourceSlot == "" && targetSlot == "" {
		return fmt.Errorf("cannot swap production with itself")
	} else if sourceSlot == "" {
		// Swapping production with a named slot (e.g., production -> staging)
		// Use BeginSwapSlotWithProduction with targetSlot as the slot to swap with
		swapRequest := armappservice.CsmSlotEntity{
			TargetSlot: to.Ptr(targetSlot),
		}
		poller, swapErr = client.BeginSwapSlotWithProduction(ctx, resourceGroup, appName, swapRequest, nil)
	} else if targetSlot == "" {
		// Swapping a named slot with production (e.g., staging -> production)
		// Use BeginSwapSlot with sourceSlot and production as target
		swapRequest := armappservice.CsmSlotEntity{
			TargetSlot: to.Ptr("production"),
		}
		poller, swapErr = client.BeginSwapSlot(ctx, resourceGroup, appName, sourceSlot, swapRequest, nil)
	} else {
		// Swapping between two named slots
		swapRequest := armappservice.CsmSlotEntity{
			TargetSlot: to.Ptr(targetSlot),
		}
		poller, swapErr = client.BeginSwapSlot(ctx, resourceGroup, appName, sourceSlot, swapRequest, nil)
	}

	if swapErr != nil {
		return fmt.Errorf("starting slot swap: %w", swapErr)
	}

	// Wait for completion
	// Type assert to get the PollUntilDone method
	switch p := poller.(type) {
	case *runtime.Poller[armappservice.WebAppsClientSwapSlotWithProductionResponse]:
		_, swapErr = p.PollUntilDone(ctx, nil)
	case *runtime.Poller[armappservice.WebAppsClientSwapSlotResponse]:
		_, swapErr = p.PollUntilDone(ctx, nil)
	default:
		return fmt.Errorf("unexpected poller type")
	}

	if swapErr != nil {
		return fmt.Errorf("waiting for slot swap to complete: %w", swapErr)
	}

	return nil
}
