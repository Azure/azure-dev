package azcli

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers"
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
		return nil, fmt.Errorf("failed retrieving container app properties: %w", err)
	}

	return &AzCliContainerAppProperties{
		HostNames: []string{*containerApp.Properties.Configuration.Ingress.Fqdn},
	}, nil
}

func (cli *azCli) createContainerAppsClient(
	ctx context.Context,
	subscriptionId string,
) (*armappcontainers.ContainerAppsClient, error) {
	options := cli.createDefaultClientOptionsBuilder(ctx).BuildArmClientOptions()
	client, err := armappcontainers.NewContainerAppsClient(subscriptionId, cli.credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating ContainerApps client: %w", err)
	}

	return client, nil
}
