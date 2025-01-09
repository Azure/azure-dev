package azapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
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
	if errors.As(err, &httpErr) && httpErr.StatusCode == 404 {
		progressLog("Resource not found. Failed to enable tracking runtime status." +
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
