package azcli

import (
	"context"
	"fmt"
	"io"

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
	webApp, err := cli.webAppsClient.Get(ctx, resourceGroup, appName, nil)
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
	response, err := cli.zipDeployClient.Deploy(ctx, appName, deployZipFile)
	if err != nil {
		return nil, err
	}

	return convert.RefOf(response.StatusText), nil
}
