package azcli

import (
	"context"
	"fmt"
	"io"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice"
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

func (cli *azCli) appServiceRepositoryHost(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	appName string,
) (string, error) {
	app, err := cli.appService(ctx, subscriptionId, resourceGroup, appName)
	if err != nil {
		return "", err
	}

	hostName := ""
	for _, item := range app.Properties.HostNameSSLStates {
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

func (cli *azCli) DeployAppServiceZip(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	appName string,
	deployZipFile io.Reader,
) (*string, error) {
	hostName, err := cli.appServiceRepositoryHost(ctx, subscriptionId, resourceGroup, appName)
	if err != nil {
		return nil, err
	}

	client, err := cli.createZipDeployClient(ctx, subscriptionId, hostName)
	if err != nil {
		return nil, err
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
