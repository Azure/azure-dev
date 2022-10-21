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
	client, err := cli.createWebAppsClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	webApp, err := client.Get(ctx, resourceGroup, appName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving webapp properties: %w", err)
	}

	return &AzCliAppServiceProperties{
		HostNames: []string{*webApp.Properties.DefaultHostName},
	}, nil
}

func (cli *azCli) DeployAppServiceZip(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	appName string,
	deployZipFile io.Reader,
) (*string, error) {
	client, err := cli.createZipDeployClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	response, err := client.Deploy(ctx, appName, deployZipFile)
	if err != nil {
		return nil, err
	}

	return convert.RefOf(response.StatusText), nil
}

func (cli *azCli) createWebAppsClient(ctx context.Context, subscriptionId string) (*armappservice.WebAppsClient, error) {
	options := cli.createDefaultClientOptionsBuilder(ctx).BuildArmClientOptions()
	client, err := armappservice.NewWebAppsClient(subscriptionId, cli.credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating WebApps client: %w", err)
	}

	return client, nil
}

func (cli *azCli) createZipDeployClient(ctx context.Context, subscriptionId string) (*azsdk.ZipDeployClient, error) {

	options := cli.createDefaultClientOptionsBuilder(ctx).BuildArmClientOptions()
	client, err := azsdk.NewZipDeployClient(subscriptionId, cli.credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating WebApps client: %w", err)
	}

	return client, nil
}
