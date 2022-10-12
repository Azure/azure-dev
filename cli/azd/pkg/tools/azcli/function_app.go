package azcli

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/convert"
)

type AzCliFunctionAppProperties struct {
	HostNames []string
}

func (cli *azCli) GetFunctionAppProperties(ctx context.Context, subscriptionId string, resourceGroup string, appName string) (*AzCliFunctionAppProperties, error) {
	client, err := cli.createWebAppsClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	webApp, err := client.Get(ctx, resourceGroup, appName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving webapp properties: %w", err)
	}

	return &AzCliFunctionAppProperties{
		HostNames: []string{*webApp.Properties.DefaultHostName},
	}, nil
}

func (cli *azCli) DeployFunctionAppUsingZipFile(ctx context.Context, subscriptionId string, resourceGroup string, appName string, deployZipPath string) (*string, error) {
	client, err := cli.createZipDeployClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	_, err = client.Deploy(ctx, appName, deployZipPath)
	if err != nil {
		return nil, err
	}

	return convert.RefOf(""), nil
}
