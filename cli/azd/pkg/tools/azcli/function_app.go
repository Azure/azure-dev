package azcli

import (
	"context"
	"io"

	"github.com/azure/azure-dev/cli/azd/pkg/convert"
)

type AzCliFunctionAppProperties struct {
	HostNames []string
}

func (cli *azCli) GetFunctionAppProperties(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	appName string,
) (*AzCliFunctionAppProperties, error) {
	webApp, err := cli.appService(ctx, subscriptionId, resourceGroup, appName)
	if err != nil {
		return nil, err
	}

	return &AzCliFunctionAppProperties{
		HostNames: []string{*webApp.Properties.DefaultHostName},
	}, nil
}

func (cli *azCli) DeployFunctionAppUsingZipFile(
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
