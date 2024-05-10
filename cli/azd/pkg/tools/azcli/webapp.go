package azcli

import (
	"context"
	"fmt"
	"io"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
)

type AzCliAppServiceProperties struct {
	HostNames []string
}

func (cli *azCli) GetAppServiceProperties(
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

func (cli *azCli) appService(
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
	if response.Properties != nil && response.Properties.SiteConfig != nil &&
		response.Properties.SiteConfig.LinuxFxVersion != nil {
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

func checkWebAppDeploymentStatus(
	res armappservice.WebAppsClientGetProductionSiteDeploymentStatusResponse,
) (string, error) {
	properties := res.CsmDeploymentStatus.Properties
	inProgressNumber := int(*properties.NumberOfInstancesInProgress)
	successNumber := int(*properties.NumberOfInstancesSuccessful)
	failNumber := int(*properties.NumberOfInstancesFailed)
	failLogs := properties.FailedInstancesLogs
	errorString := ""
	logErrorFunction := func(properties *armappservice.CsmDeploymentStatusProperties, message string) {
		for _, err := range properties.Errors {
			if err.Message != nil {
				errorString += fmt.Sprintf("Error: %s\n", *err.Message)
			}
		}

		for _, log := range failLogs {
			errorString += fmt.Sprintf("Please check the %slogs for more info: %s\n", message, *log)
		}
	}

	switch *properties.Status {
	case armappservice.DeploymentBuildStatusRuntimeSuccessful:
		return "", nil
	case armappservice.DeploymentBuildStatusRuntimeFailed:
		totalNumber := inProgressNumber + successNumber + failNumber

		if successNumber > 0 {
			errorString += fmt.Sprintf("Site started with errors: %d/%d instances failed to start successfully\n",
				failNumber, totalNumber)
		} else if totalNumber > 0 {
			errorString += fmt.Sprintf("Deployment failed because the runtime process failed. In progress instances: %d, "+
				"Successful instances: %d, Failed Instances: %d\n",
				inProgressNumber, successNumber, failNumber)
		}

		logErrorFunction(properties, "runtime ")
		return "", fmt.Errorf(errorString)
	case armappservice.DeploymentBuildStatusBuildFailed:
		errorString += "Deployment failed because the build process failed\n"
		logErrorFunction(properties, "build ")
		return "", fmt.Errorf(errorString)
	// Default case for the rest statuses, they shouldn't appear as a final response
	default:
		errorString += fmt.Sprintf("Deployment failed with status: %s\n", *properties.Status)
		logErrorFunction(properties, "")
		return "", fmt.Errorf(errorString)
	}
}

func (cli *azCli) DeployAppServiceZip(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	appName string,
	deployZipFile io.Reader,
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
		response, err := client.DeployTrackStatus(ctx, deployZipFile, subscriptionId, resourceGroup, appName)
		if err != nil {
			return nil, err
		}

		res, err := checkWebAppDeploymentStatus(response)
		if err != nil {
			return nil, err
		}

		return &res, nil
	}

	response, err := client.Deploy(ctx, deployZipFile)
	if err != nil {
		return nil, err
	}

	return convert.RefOf(response.StatusText), nil
}

func (cli *azCli) createWebAppsClient(ctx context.Context, subscriptionId string) (*armappservice.WebAppsClient, error) {
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

func (cli *azCli) createZipDeployClient(
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
