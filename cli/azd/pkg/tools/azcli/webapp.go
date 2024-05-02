package azcli

import (
	"context"
	"fmt"
	"io"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
)

var (
	deploymentBuildStatusBuildFailed       armappservice.DeploymentBuildStatus = "BuildFailed"
	deploymentBuildStatusRuntimeFailed     armappservice.DeploymentBuildStatus = "RuntimeFailed"
	deploymentBuildStatusRuntimeSuccessful armappservice.DeploymentBuildStatus = "RuntimeSuccessful"
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

func (cli *azCli) appServiceRepositoryHost(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	appName string,
) (string, bool, error) {
	linuxWebApp := false
	app, err := cli.appService(ctx, subscriptionId, resourceGroup, appName)
	if err != nil {
		return "", linuxWebApp, err
	}

	if app.Properties.SiteConfig.LinuxFxVersion != nil {
		linuxWebApp = true
	}

	hostName := ""
	for _, item := range app.Properties.HostNameSSLStates {
		if *item.HostType == armappservice.HostTypeRepository {
			hostName = *item.Name
			break
		}
	}

	if hostName == "" {
		return "", linuxWebApp, fmt.Errorf("failed to find host name for webapp %s", appName)
	}

	return hostName, linuxWebApp, nil
}

func checkRunTimeStatus(res armappservice.WebAppsClientGetProductionSiteDeploymentStatusResponse) (string, error) {
	properties := res.CsmDeploymentStatus.Properties
	status := properties.Status
	deploymentResult := ""
	inProgressNumber := int(*properties.NumberOfInstancesInProgress)
	successNumber := int(*properties.NumberOfInstancesSuccessful)
	failNumber := int(*properties.NumberOfInstancesFailed)
	totalNumber := inProgressNumber + successNumber + failNumber
	failLog := properties.FailedInstancesLogs
	errorString := ""
	var errorExtendedCode *string
	var errorMessage *string

	switch *status {
	case deploymentBuildStatusBuildFailed:
		errorString += "Deployment failed because the build process failed\n"
		errors := properties.Errors

		if len(errors) > 0 {
			errorExtendedCode = errors[0].ExtendedCode
			errorMessage = errors[0].Message

			if errorMessage != nil {
				errorString += fmt.Sprintf("Error: %s\n", *errorMessage)
			} else if errorExtendedCode != nil {
				errorString += fmt.Sprintf("Extended ErrorCode: %s\n", *errorExtendedCode)
			}
		}

		if len(failLog) > 0 {
			errorString += fmt.Sprintf("Please check the build logs for more info: %s\n", *failLog[0])
		}

		return "", fmt.Errorf(errorString)
	case deploymentBuildStatusRuntimeFailed:
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
			errorExtendedCode = errors[0].ExtendedCode
			errorMessage = errors[0].Message

			if errorMessage != nil {
				errorString += fmt.Sprintf("Error: %s\n", *errorMessage)
			} else if errorExtendedCode != nil {
				errorString += fmt.Sprintf("Extended ErrorCode: %s\n", *errorExtendedCode)
			}
		}

		if len(failLog) > 0 {
			errorString += fmt.Sprintf("Please check the runtime logs for more info: %s\n", *failLog[0])
		}

		return "", fmt.Errorf(errorString)
	case deploymentBuildStatusRuntimeSuccessful:
		return "", nil
	}

	return deploymentResult, nil
}

func (cli *azCli) DeployAppServiceZip(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	appName string,
	deployZipFile io.Reader,
) (*string, error) {
	hostName, linuxWebApp, err := cli.appServiceRepositoryHost(ctx, subscriptionId, resourceGroup, appName)
	if err != nil {
		return nil, err
	}

	client, err := cli.createZipDeployClient(ctx, subscriptionId, hostName)
	if err != nil {
		return nil, err
	}

	// Deployment Status API only support linux web app for now
	if linuxWebApp {
		credential, err := cli.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
		if err != nil {
			return nil, err
		}

		response, err := client.DeployTrackStatus(ctx, deployZipFile, credential, subscriptionId, resourceGroup, appName)
		if err != nil {
			return nil, err
		}

		res, err := checkRunTimeStatus(response)
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
