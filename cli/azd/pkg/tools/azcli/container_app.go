package azcli

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
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

	var hostNames []string
	if containerApp.Properties != nil &&
		containerApp.Properties.Configuration != nil &&
		containerApp.Properties.Configuration.Ingress != nil &&
		containerApp.Properties.Configuration.Ingress.Fqdn != nil {
		hostNames = []string{*containerApp.Properties.Configuration.Ingress.Fqdn}
	} else {
		hostNames = []string{}
	}

	return &AzCliContainerAppProperties{
		HostNames: hostNames,
	}, nil
}

func (cli *azCli) createContainerAppsClient(
	ctx context.Context,
	subscriptionId string,
) (*armappcontainers.ContainerAppsClient, error) {
	options := cli.createDefaultClientOptionsBuilder(ctx).BuildArmClientOptions()
	credential, err := cli.credentialProvider(ctx, &auth.CredentialForCurrentUserOptions{
		TenantID: cli.tenantId,
	})
	if err != nil {
		return nil, err
	}
	client, err := armappcontainers.NewContainerAppsClient(subscriptionId, credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating ContainerApps client: %w", err)
	}

	return client, nil
}
