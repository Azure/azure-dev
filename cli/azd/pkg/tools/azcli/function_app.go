package azcli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
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

	cred, err := cli.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	plansClient, err := armappservice.NewPlansClient(subscriptionId, cred, cli.armClientOptions)
	if err != nil {
		return nil, err
	}

	planId := *app.Properties.ServerFarmID
	sep := strings.LastIndexByte(planId, '/')
	if sep == -1 {
		return nil, fmt.Errorf("unexpected ServerFarmID %s", planId)
	}

	planName := planId[sep+1:]
	plan, err := plansClient.Get(ctx, resourceGroup, planName, nil)
	if err != nil {
		return nil, err
	}

	if strings.ToLower(*plan.SKU.Tier) == "flexconsumption" {
		client, err := azsdk.NewFuncAppHostClient(hostName, cred, cli.armClientOptions)
		if err != nil {
			return nil, fmt.Errorf("creating func app host client: %w", err)
		}

		response, err := client.Publish(ctx, deployZipFile, &azsdk.PublishOptions{RemoteBuild: remoteBuild})
		if err != nil {
			return nil, fmt.Errorf("publishing zip file: %w", err)
		}
		return convert.RefOf(response.StatusText), nil
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
