// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v2"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
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

	if httpErr, ok := errors.AsType[*azcore.ResponseError](err); ok && httpErr.StatusCode == http.StatusNotFound {
		progressLog("Resource not found. Failed to enable tracking runtime status." +
			"Resuming deployment without tracking status.")
		return true
	}

	if httpErr, ok := errors.AsType[*azcore.ResponseError](err); ok &&
		httpErr.StatusCode == http.StatusInternalServerError {
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
) (_ *string, err error) {
	ctx, span := tracing.Start(ctx, events.DeployAppServiceZipEvent)
	defer func() { span.EndWithStatus(err) }()

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

	isLinux := isLinuxWebApp(app)
	span.SetAttributes(fields.DeployLinuxKey.Key.Bool(isLinux))

	// Deployment Status API only support linux web app for now
	if isLinux {
		// Build failures can be caused by an SCM container restart triggered by ARM
		// applying site config (app settings) shortly after the site is created.
		// Due to eventual consistency in the Azure platform, the SCM container may
		// still be restarting even after provisioning reports success. Retry the
		// entire zip deploy when the build fails, giving the SCM time to stabilize.
		const maxBuildRetries = 2
		for attempt := range maxBuildRetries + 1 {
			span.SetAttributes(fields.DeployAttemptKey.Key.Int(attempt + 1))

			if attempt > 0 {
				// Exponential backoff: 5s, 10s between retries to avoid hammering
				// the SCM endpoint while it stabilizes.
				retryDelay := time.Duration(attempt) * 5 * time.Second
				select {
				case <-time.After(retryDelay):
				case <-ctx.Done():
					return nil, ctx.Err()
				}

				// Reset the zip file reader so the retry re-uploads the full content.
				if _, seekErr := deployZipFile.Seek(0, io.SeekStart); seekErr != nil {
					return nil, fmt.Errorf("resetting zip file for retry: %w", seekErr)
				}
				progressLog(fmt.Sprintf(
					"Retrying deployment (attempt %d/%d) — the previous build may have been "+
						"interrupted by an SCM container restart", attempt+1, maxBuildRetries+1))

				// Wait for the SCM site to become ready before retrying.
				if waitErr := waitForScmReady(ctx, client, 5*time.Second, progressLog); waitErr != nil {
					// Only propagate if the caller's own context is cancelled (e.g. Ctrl+C).
					// Do not propagate context errors from waitForScmReady's internal timeout.
					if ctx.Err() != nil {
						return nil, waitErr
					}
					log.Printf("SCM readiness check failed (non-fatal): %v", waitErr)
				}
			}

			err = client.DeployTrackStatus(
				ctx, deployZipFile, subscriptionId, resourceGroup, appName, progressLog)
			if err != nil {
				if isBuildFailure(err) && attempt < maxBuildRetries {
					progressLog("Build process failed — will retry after SCM stabilizes")
					continue
				}
				if !resumeDeployment(err, progressLog) {
					return nil, err
				}
			} else {
				// Deployment is successful
				return new("OK"), nil
			}
			break
		}
	}

	// Rewind the zip file before the fallback deploy — the tracked deploy consumed it.
	if _, seekErr := deployZipFile.Seek(0, io.SeekStart); seekErr != nil {
		return nil, fmt.Errorf("resetting zip file for fallback deploy: %w", seekErr)
	}

	response, err := client.Deploy(ctx, deployZipFile)
	if err != nil {
		return nil, err
	}

	return &response.StatusText, nil
}

// isBuildFailure returns true when the deployment error indicates a transient
// Oryx/Kudu build failure caused by an SCM container restart — not a genuine
// application build error. We detect the transient case by matching the exact
// prefix "the build process failed" (which comes from the Kudu deployment API)
// while excluding messages from the deployment status API that start with
// "Deployment failed because the build process failed" (genuine build errors).
//
// NOTE: This heuristic is tied to exact Azure/Kudu error message wording.
// If the upstream messages change, detection breaks silently and retries stop
// occurring (falling back to the non-retry deploy path). The Azure SDK does
// not surface structured error codes for these build failures.
func isBuildFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// Genuine build failures from logWebAppDeploymentStatus start with
	// "Deployment failed because the build process failed". Transient SCM
	// failures use the shorter "the build process failed" without that prefix.
	return strings.Contains(msg, "the build process failed") &&
		!strings.Contains(msg, "Deployment failed because") &&
		!strings.Contains(msg, "logs for more info")
}

// scmReadyChecker abstracts the IsScmReady probe so waitForScmReady can be
// unit-tested with a mock. *azsdk.ZipDeployClient satisfies this interface.
type scmReadyChecker interface {
	IsScmReady(ctx context.Context) (bool, error)
}

// waitForScmReady pings the SCM /api/deployments endpoint until it responds
// with 200, indicating the SCM container has finished restarting. This avoids
// pushing a new zip deploy into a container that is about to restart.
// pollInterval controls the polling frequency; callers pass the production value
// (typically 5s) while tests can use shorter intervals to avoid wall-time delays.
func waitForScmReady(
	parentCtx context.Context, client scmReadyChecker, pollInterval time.Duration, progressLog func(string),
) error {
	const scmReadyTimeout = 90 * time.Second

	ctx, cancel := context.WithTimeout(parentCtx, scmReadyTimeout)
	defer cancel()

	progressLog("Waiting for SCM site to become ready...")

	// Probe once immediately to avoid a needless pollInterval delay when SCM is already ready.
	ready, err := client.IsScmReady(ctx)
	if err == nil && ready {
		progressLog("SCM site is ready")
		return nil
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Distinguish parent cancellation (Ctrl+C) from local timeout expiry.
			if parentCtx.Err() != nil {
				return parentCtx.Err()
			}
			return fmt.Errorf("SCM site did not become ready within %v: %w", scmReadyTimeout, ctx.Err())
		case <-ticker.C:
		}

		ready, err := client.IsScmReady(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return err
			}
			log.Printf("SCM readiness probe error: %v", err)
			continue
		}
		if ready {
			progressLog("SCM site is ready")
			return nil
		}
	}
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

	return &response.StatusText, nil
}
