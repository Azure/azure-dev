package azapi

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
)

type AzCliFunctionAppProperties struct {
	HostNames []string
}

func (cli *AzureClient) GetFunctionAppProperties(
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

func (cli *AzureClient) DeployFunctionAppUsingZipFile(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	appName string,
	deployZipFile io.ReadSeeker,
	remoteBuild bool,
) (*string, error) {
	app, err := cli.appService(ctx, subscriptionId, resourceGroup, appName)
	if err != nil {
		return nil, err
	}

	hostName, err := appServiceRepositoryHost(app, appName)
	if err != nil {
		return nil, err
	}

	planId, err := arm.ParseResourceID(*app.Properties.ServerFarmID)
	if err != nil {
		return nil, err
	}

	plansCred, err := cli.credentialProvider.CredentialForSubscription(ctx, planId.SubscriptionID)
	if err != nil {
		return nil, err
	}

	plansClient, err := armappservice.NewPlansClient(planId.SubscriptionID, plansCred, cli.armClientOptions)
	if err != nil {
		return nil, err
	}

	plan, err := plansClient.Get(ctx, planId.ResourceGroupName, planId.Name, nil)
	if err != nil {
		return nil, err
	}

	if strings.ToLower(*plan.SKU.Tier) == "flexconsumption" {
		cred, err := cli.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
		if err != nil {
			return nil, err
		}

		client, err := azsdk.NewFuncAppHostClient(hostName, cred, cli.armClientOptions)
		if err != nil {
			return nil, fmt.Errorf("creating func app host client: %w", err)
		}

		response, err := client.Publish(ctx, deployZipFile, &azsdk.PublishOptions{RemoteBuild: remoteBuild})
		if err != nil {
			return nil, fmt.Errorf("publishing zip file: %w", err)
		}
		return to.Ptr(response.StatusText), nil
	}

	client, err := cli.createZipDeployClient(ctx, subscriptionId, hostName)
	if err != nil {
		return nil, err
	}

	response, err := client.Deploy(ctx, deployZipFile)
	if err != nil {
		return nil, err
	}

	return to.Ptr(response.StatusText), nil
}
