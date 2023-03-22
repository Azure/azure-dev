package azcli

import (
	"context"
	"fmt"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appplatform/armappplatform"
)

type AzCliSpringAppProperties struct {
	HostNames []string
}

func (cli *azCli) GetSpringAppProperties(
	ctx context.Context,
	subscriptionId, resourceGroup, appName string,
) (*AzCliSpringAppProperties, error) {
	client, err := cli.createSpringAppClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	// use "default" app name
	springApp, err := client.Get(ctx, resourceGroup, appName, "default", nil)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving spring apps properties: %w", err)
	}

	return &AzCliSpringAppProperties{
		HostNames: []string{*springApp.Properties.Fqdn},
	}, nil
}

func (cli *azCli) createSpringAppClient(
	ctx context.Context,
	subscriptionId string,
) (*armappplatform.AppsClient, error) {
	options := cli.createDefaultClientOptionsBuilder(ctx).BuildArmClientOptions()
	client, err := armappplatform.NewAppsClient(subscriptionId, cli.credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating SpringApp client: %w", err)
	}

	return client, nil
}
