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

func (cli *azCli) appServiceRepositoryHost(
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

func checkWebAppDeploymentStatus(res armappservice.WebAppsClientGetProductionSiteDeploymentStatusResponse,
	printStatus func(string),
) (string, error) {
	properties := res.CsmDeploymentStatus.Properties
	deploymentResult := ""
	inProgressNumber := int(*properties.NumberOfInstancesInProgress)
	successNumber := int(*properties.NumberOfInstancesSuccessful)
	failNumber := int(*properties.NumberOfInstancesFailed)
	totalNumber := inProgressNumber + successNumber + failNumber
	failLogs := properties.FailedInstancesLogs
	errorString := ""

	switch *properties.Status {
	case armappservice.DeploymentBuildStatusRuntimeStarting:
		printStatus(fmt.Sprintf("Starting deployment. In progress instances: %d, successful instances: %d",
			inProgressNumber, successNumber))
	case armappservice.DeploymentBuildStatusRuntimeSuccessful:
		return "", nil
	case armappservice.DeploymentBuildStatusRuntimeFailed:
		if successNumber > 0 {
			errorString += fmt.Sprintf("Site started with errors: %d/%d instances failed to start successfully\n",
				failNumber, totalNumber)
		} else if totalNumber > 0 {
			errorString += fmt.Sprintf("Deployment failed because the runtime process failed. In progress instances: %d, "+
				"Successful instances: %d, Failed Instances: %d\n",
				inProgressNumber, successNumber, failNumber)
		}

		errors := properties.Errors

		if len(errors) > 0 {
			for _, err := range errors {
				if err.Message != nil {
					errorString += fmt.Sprintf("Error: %s\n", *err.Message)
				}
			}
		}

		if len(failLogs) > 0 {
			for _, log := range failLogs {
				errorString += fmt.Sprintf("Please check the runtime logs for more info: %s\n", *log)
			}
		}

		return "", fmt.Errorf(errorString)
	case armappservice.DeploymentBuildStatusBuildFailed:
		errorString += "Deployment failed because the build process failed\n"
		errors := properties.Errors

		if len(errors) > 0 {
			for _, err := range errors {
				if err.Message != nil {
					errorString += fmt.Sprintf("Error: %s\n", *err.Message)
				}
			}
		}

		if len(failLogs) > 0 {
			for _, log := range failLogs {
				errorString += fmt.Sprintf("Please check the build logs for more info: %s\n", *log)
			}
		}

		return "", fmt.Errorf(errorString)
	}

	return deploymentResult, nil
}

func (cli *azCli) DeployAppServiceZip(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	appName string,
	deployZipFile io.Reader,
	printStatus func(string),
) (*string, error) {
	app, err := cli.appService(ctx, subscriptionId, resourceGroup, appName)
	if err != nil {
		return nil, err
	}

	hostName, err := cli.appServiceRepositoryHost(app, appName)
	if err != nil {
		return nil, err
	}

	linuxWebApp := isLinuxWebApp(app)

	client, err := cli.createZipDeployClient(ctx, subscriptionId, hostName)
	if err != nil {
		return nil, err
	}

	// Deployment Status API only support linux web app for now
	if linuxWebApp {
		printStatus("Tracking deployment status")
		response, err := client.DeployTrackStatus(ctx, deployZipFile, subscriptionId, resourceGroup, appName, printStatus)
		if err != nil {
			return nil, err
		}

		res, err := checkWebAppDeploymentStatus(response, printStatus)
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
