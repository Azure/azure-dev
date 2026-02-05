// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

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

	// Get current environment
	envClient := azdClient.Environment()
	currentEnv, err := envClient.GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return fmt.Errorf("failed to get current environment: %w", err)
	}

	// Get subscription ID from environment
	subscriptionResp, err := envClient.GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: currentEnv.Environment.Name,
		Key:     "AZURE_SUBSCRIPTION_ID",
	})
	if err != nil {
		return fmt.Errorf("failed to get AZURE_SUBSCRIPTION_ID: %w", err)
	}

	subscriptionId := subscriptionResp.Value
	if subscriptionId == "" {
		return fmt.Errorf("AZURE_SUBSCRIPTION_ID environment variable is required")
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

	// Get the target resource (resource group and app name) using azd's discovery logic
	// This handles both explicit configuration and tag-based discovery
	targetResourceResp, err := azdClient.Project().GetServiceTargetResource(ctx, &azdext.GetServiceTargetResourceRequest{
		ServiceName: selectedService.Name,
	})
	if err != nil {
		return fmt.Errorf("failed to resolve target resource for service '%s': %w", selectedService.Name, err)
	}

	resourceGroup := targetResourceResp.TargetResource.ResourceGroupName
	if resourceGroup == "" {
		return fmt.Errorf("resource group not found for service '%s'", selectedService.Name)
	}

	appName := targetResourceResp.TargetResource.ResourceName
	if appName == "" {
		return fmt.Errorf("resource name not found for service '%s'. "+
			"Ensure the service has been provisioned or configure 'resourceName' in azure.yaml", selectedService.Name)
	}

	// Get tenant ID for the subscription
	tenantResponse, err := azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
		SubscriptionId: subscriptionId,
	})
	if err != nil {
		return fmt.Errorf("failed to get tenant ID: %w", err)
	}

	// Create Azure credential using AzureDeveloperCLICredential
	credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID: tenantResponse.TenantId,
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

	// Perform the swap (spinner will show progress)
	err = swapSlot(ctx, client, resourceGroup, appName, srcSlot, dstSlot)
	if err != nil {
		return fmt.Errorf("swapping slots: %w", err)
	}

	color.Green("✓ Swap completed successfully: %s ↔ %s", srcDisplay, dstDisplay)
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

// spinner represents a simple terminal spinner with status updates
type spinner struct {
	frames   []string
	interval time.Duration
	message  string
	mu       sync.Mutex
	stop     chan struct{}
	done     chan struct{}
}

// newSpinner creates a new spinner instance
func newSpinner(message string) *spinner {
	return &spinner{
		frames:   []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		interval: 100 * time.Millisecond,
		message:  message,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// start begins the spinner animation
func (s *spinner) start() {
	go func() {
		defer close(s.done)
		frameIdx := 0
		for {
			select {
			case <-s.stop:
				// Clear the line before exiting
				fmt.Print("\r\033[K")
				return
			default:
				s.mu.Lock()
				msg := s.message
				s.mu.Unlock()

				frame := s.frames[frameIdx%len(s.frames)]
				fmt.Printf("\r%s %s", color.CyanString(frame), msg)
				frameIdx++
				time.Sleep(s.interval)
			}
		}
	}()
}

// updateMessage updates the spinner message
func (s *spinner) updateMessage(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.message = message
}

// stopSpinner stops the spinner and waits for it to finish
func (s *spinner) stopSpinner() {
	close(s.stop)
	<-s.done
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
	// Get display names for progress messages
	srcDisplay := sourceSlot
	if srcDisplay == "" {
		srcDisplay = "production"
	}
	dstDisplay := targetSlot
	if dstDisplay == "" {
		dstDisplay = "production"
	}

	// Start the spinner
	spin := newSpinner(fmt.Sprintf("Initiating swap: %s ↔ %s", srcDisplay, dstDisplay))
	spin.start()
	defer spin.stopSpinner()

	// Handle the swap based on which slots are involved
	if sourceSlot == "" && targetSlot == "" {
		return fmt.Errorf("cannot swap production with itself")
	}

	var swapErr error

	if sourceSlot == "" {
		// Swapping production with a named slot (e.g., production -> staging)
		swapRequest := armappservice.CsmSlotEntity{
			TargetSlot: to.Ptr(targetSlot),
		}
		poller, err := client.BeginSwapSlotWithProduction(ctx, resourceGroup, appName, swapRequest, nil)
		if err != nil {
			return fmt.Errorf("starting slot swap: %w", err)
		}
		swapErr = pollWithProgress(ctx, poller, spin, srcDisplay, dstDisplay)
	} else if targetSlot == "" {
		// Swapping a named slot with production (e.g., staging -> production)
		swapRequest := armappservice.CsmSlotEntity{
			TargetSlot: to.Ptr("production"),
		}
		poller, err := client.BeginSwapSlot(ctx, resourceGroup, appName, sourceSlot, swapRequest, nil)
		if err != nil {
			return fmt.Errorf("starting slot swap: %w", err)
		}
		swapErr = pollWithProgress(ctx, poller, spin, srcDisplay, dstDisplay)
	} else {
		// Swapping between two named slots
		swapRequest := armappservice.CsmSlotEntity{
			TargetSlot: to.Ptr(targetSlot),
		}
		poller, err := client.BeginSwapSlot(ctx, resourceGroup, appName, sourceSlot, swapRequest, nil)
		if err != nil {
			return fmt.Errorf("starting slot swap: %w", err)
		}
		swapErr = pollWithProgress(ctx, poller, spin, srcDisplay, dstDisplay)
	}

	if swapErr != nil {
		return fmt.Errorf("waiting for slot swap to complete: %w", swapErr)
	}

	return nil
}

// pollWithProgress polls the operation and updates the spinner with status information
func pollWithProgress[T any](
	ctx context.Context,
	poller *runtime.Poller[T],
	spin *spinner,
	srcSlot string,
	dstSlot string,
) error {
	pollCount := 0
	startTime := time.Now()

	for !poller.Done() {
		pollCount++
		elapsed := time.Since(startTime).Round(time.Second)

		// Update spinner with progress information
		statusMsg := getProgressMessage(pollCount, elapsed, srcSlot, dstSlot)
		spin.updateMessage(statusMsg)

		// Poll the operation
		resp, err := poller.Poll(ctx)
		if err != nil {
			return err
		}

		// Try to extract status from response headers or body
		if resp != nil {
			status := extractStatus(resp)
			if status != "" {
				spin.updateMessage(fmt.Sprintf("%s [%s]", statusMsg, status))
			}
		}

		// Wait before next poll (Azure typically recommends waiting between polls)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
			// Continue polling
		}
	}

	return nil
}

// getProgressMessage returns a progress message based on the poll count and elapsed time
func getProgressMessage(pollCount int, elapsed time.Duration, srcSlot, dstSlot string) string {
	phases := []string{
		"Preparing swap",
		"Warming up target slot",
		"Applying configuration",
		"Swapping virtual IPs",
		"Verifying swap",
		"Finalizing",
	}

	// Estimate phase based on poll count (rough approximation)
	phaseIdx := pollCount / 3
	if phaseIdx >= len(phases) {
		phaseIdx = len(phases) - 1
	}

	return fmt.Sprintf("%s: %s ↔ %s (%v)", phases[phaseIdx], srcSlot, dstSlot, elapsed)
}

// extractStatus tries to extract status information from the HTTP response
func extractStatus(resp *http.Response) string {
	if resp == nil {
		return ""
	}

	// Check for Azure-AsyncOperation or Location header status
	if status := resp.Header.Get("x-ms-request-status"); status != "" {
		return status
	}

	// Check provisioning state from response body if available
	// Note: We don't want to consume the body here as it may be needed later
	// Just return the HTTP status for now
	switch resp.StatusCode {
	case http.StatusOK:
		return "Completed"
	case http.StatusAccepted:
		return "In Progress"
	case http.StatusCreated:
		return "Created"
	default:
		return ""
	}
}
