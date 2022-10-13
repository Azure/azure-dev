package azcli

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice"
	"github.com/azure/azure-dev/cli/azd/pkg/identity"
)

type AzCliContainerAppProperties struct {
	HostNames []string
}

func (cli *azCli) GetContainerAppProperties(
	ctx context.Context,
	subscriptionId, resourceGroup, appName string,
) (*AzCliContainerAppProperties, error) {
	client, err := cli.createContainerAppsClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	containerApp, err := client.Get(ctx, resourceGroup, appName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving webapp properties: %w", err)
	}

	return &AzCliContainerAppProperties{
		HostNames: []string{*containerApp.Properties.Configuration.Ingress.Fqdn},
	}, nil
}

func (cli *azCli) createContainerAppsClient(
	ctx context.Context,
	subscriptionId string,
) (*armappservice.ContainerAppsClient, error) {
	cred, err := identity.GetCredentials(ctx)
	if err != nil {
		return nil, err
	}

	options := cli.createDefaultClientOptionsBuilder(ctx).BuildArmClientOptions()
	client, err := armappservice.NewContainerAppsClient(subscriptionId, cred, options)
	if err != nil {
		return nil, fmt.Errorf("creating ContainerApps client: %w", err)
	}

	return client, nil
}
