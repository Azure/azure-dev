// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
)

type AzCliAppServiceProperties struct {
	HostNames []string
}

func (cli *AzureClient) GetAppServiceProperties(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	appName string,
) (*AzCliAppServiceProperties, error) {
	webApp, err := cli.appService(ctx, subscriptionId, resourceGroup, appName)
	if err != nil {
		return nil, err
	}

	return &AzCliAppServiceProperties{
		HostNames: []string{*webApp.Properties.DefaultHostName},
	}, nil
}

func (cli *AzureClient) GetAppServiceSlotProperties(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	appName string,
	slotName string,
) (*AzCliAppServiceProperties, error) {
	client, err := cli.createWebAppsClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	slot, err := client.GetSlot(ctx, resourceGroup, appName, slotName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving webapp slot properties: %w", err)
	}

	return &AzCliAppServiceProperties{
		HostNames: []string{*slot.Properties.DefaultHostName},
	}, nil
}

func (cli *AzureClient) appService(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	appName string,
) (*armappservice.WebAppsClientGetResponse, error) {
	client, err := cli.createWebAppsClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	webApp, err := client.Get(ctx, resourceGroup, appName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving webapp properties: %w", err)
	}

	return &webApp, nil
}

func isLinuxWebApp(response *armappservice.WebAppsClientGetResponse) bool {
	if *response.Kind == "app,linux" && response.Properties != nil && response.Properties.SiteConfig != nil &&
		response.Properties.SiteConfig.LinuxFxVersion != nil &&
		*response.Properties.SiteConfig.LinuxFxVersion != "" {
		return true
	}
	return false
}

func appServiceRepositoryHost(
	response *armappservice.WebAppsClientGetResponse,
	appName string,
) (string, error) {
	hostName := ""
	for _, item := range response.Properties.HostNameSSLStates {
		if *item.HostType == armappservice.HostTypeRepository {
			hostName = *item.Name
			break
		}
	}

	if hostName == "" {
		return "", fmt.Errorf("failed to find host name for webapp %s", appName)
	}

	return hostName, nil
}

func resumeDeployment(err error, progressLog func(msg string)) bool {
	errorMessage := err.Error()
	if strings.Contains(errorMessage, "empty deployment status id") {
		progressLog("Deployment status id is empty. Failed to enable tracking runtime status." +
			"Resuming deployment without tracking status.")
		return true
	}

	if strings.Contains(errorMessage, "response or its properties are empty") {
		progressLog("Response or its properties are empty. Failed to enable tracking runtime status." +
			"Resuming deployment without tracking status.")
		return true
	}

	if strings.Contains(errorMessage, "failed to start within the allotted time") {
		progressLog("Deployment with tracking status failed to start within the allotted time." +
			"Resuming deployment without tracking status.")
		return true
	}

	if strings.Contains(errorMessage, "the build process failed") && !strings.Contains(errorMessage, "logs for more info") {
		progressLog("Failed to enable tracking runtime status." +
			"Resuming deployment without tracking status.")
		return true
	}

	var httpErr *azcore.ResponseError
	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
		progressLog("Resource not found. Failed to enable tracking runtime status." +
			"Resuming deployment without tracking status.")
		return true
	}

	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusInternalServerError {
		progressLog("Internal server error. Failed to enable tracking runtime status. " +
			"Resuming deployment without tracking status.")
		return true
	}
	return false
}

func (cli *AzureClient) DeployAppServiceZip(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	appName string,
	deployZipFile io.ReadSeeker,
	progressLog func(string),
) (*string, error) {
	app, err := cli.appService(ctx, subscriptionId, resourceGroup, appName)
	if err != nil {
		return nil, err
	}

	hostName, err := appServiceRepositoryHost(app, appName)
	if err != nil {
		return nil, err
	}

	client, err := cli.createZipDeployClient(ctx, subscriptionId, hostName)
	if err != nil {
		return nil, err
	}

	// Deployment Status API only support linux web app for now
	if isLinuxWebApp(app) {
		if err := client.DeployTrackStatus(
			ctx, deployZipFile, subscriptionId, resourceGroup, appName, progressLog); err != nil {
			if !resumeDeployment(err, progressLog) {
				return nil, err
			}
		} else {
			// Deployment is successful
			statusText := "OK"
			return to.Ptr(statusText), nil
		}
	}

	response, err := client.Deploy(ctx, deployZipFile)
	if err != nil {
		return nil, err
	}

	return to.Ptr(response.StatusText), nil
}

func (cli *AzureClient) createWebAppsClient(
	ctx context.Context,
	subscriptionId string,
) (*armappservice.WebAppsClient, error) {
	credential, err := cli.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	client, err := armappservice.NewWebAppsClient(subscriptionId, credential, cli.armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating WebApps client: %w", err)
	}

	return client, nil
}

func (cli *AzureClient) createZipDeployClient(
	ctx context.Context,
	subscriptionId string,
	hostName string,
) (*azsdk.ZipDeployClient, error) {
	credential, err := cli.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	client, err := azsdk.NewZipDeployClient(hostName, credential, cli.armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating WebApps client: %w", err)
	}

	return client, nil
}

// HasAppServiceDeployments checks if the web app has at least one previous deployment.
func (cli *AzureClient) HasAppServiceDeployments(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	appName string,
) (bool, error) {
	client, err := cli.createWebAppsClient(ctx, subscriptionId)
	if err != nil {
		return false, err
	}

	pager := client.NewListDeploymentsPager(resourceGroup, appName, nil)
	if pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return false, fmt.Errorf("listing webapp deployments: %w", err)
		}
		return len(page.Value) > 0, nil
	}

	return false, nil
}

// AppServiceSlot represents an App Service deployment slot.
type AppServiceSlot struct {
	Name string
}

// GetAppServiceSlots returns a list of deployment slots for the specified web app.
func (cli *AzureClient) GetAppServiceSlots(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	appName string,
) ([]AppServiceSlot, error) {
	client, err := cli.createWebAppsClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	var slots []AppServiceSlot
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
				slots = append(slots, AppServiceSlot{Name: slotName})
			}
		}
	}

	return slots, nil
}

// DeployAppServiceSlotZip deploys a zip file to a specific deployment slot.
func (cli *AzureClient) DeployAppServiceSlotZip(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	appName string,
	slotName string,
	deployZipFile io.ReadSeeker,
	progressLog func(string),
) (*string, error) {
	client, err := cli.createWebAppsClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	slot, err := client.GetSlot(ctx, resourceGroup, appName, slotName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving webapp slot: %w", err)
	}

	// Find the repository hostname for the slot
	hostName := ""
	if slot.Properties != nil {
		for _, item := range slot.Properties.HostNameSSLStates {
			if item != nil && item.HostType != nil && item.Name != nil &&
				*item.HostType == armappservice.HostTypeRepository {
				hostName = *item.Name
				break
			}
		}
	}

	if hostName == "" {
		return nil, fmt.Errorf("failed to find repository host name for slot %s", slotName)
	}

	zipDeployClient, err := cli.createZipDeployClient(ctx, subscriptionId, hostName)
	if err != nil {
		return nil, err
	}

	response, err := zipDeployClient.Deploy(ctx, deployZipFile)
	if err != nil {
		return nil, err
	}

	return to.Ptr(response.StatusText), nil
}

// SwapSlot swaps two deployment slots or a slot with production.
// sourceSlot: the source slot name (empty string means production)
// targetSlot: the target slot name (empty string means production)
// The swap operation swaps the content of sourceSlot into targetSlot.
func (cli *AzureClient) SwapSlot(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	appName string,
	sourceSlot string,
	targetSlot string,
) error {
	client, err := cli.createWebAppsClient(ctx, subscriptionId)
	if err != nil {
		return err
	}

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
