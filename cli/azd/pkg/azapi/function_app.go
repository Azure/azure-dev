// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"fmt"
	"io"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
)

// AzCliFunctionAppProperties contains properties for a Function App.
type AzCliFunctionAppProperties struct {
	HostNames         []string
	ServerFarmID      string
	HostNameSslStates []*armappservice.HostNameSSLState
}

// GetFunctionAppProperties retrieves properties for a function app.
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
		HostNames:         []string{*webApp.Properties.DefaultHostName},
		ServerFarmID:      *webApp.Properties.ServerFarmID,
		HostNameSslStates: webApp.Properties.HostNameSSLStates,
	}, nil
}

// GetFunctionAppPlan retrieves the app service plan for a function app using pre-fetched properties.
func (cli *AzureClient) GetFunctionAppPlan(
	ctx context.Context,
	props *AzCliFunctionAppProperties,
) (*armappservice.Plan, error) {
	planId, err := arm.ParseResourceID(props.ServerFarmID)
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

	planResp, err := plansClient.Get(ctx, planId.ResourceGroupName, planId.Name, nil)
	if err != nil {
		return nil, err
	}

	return &planResp.Plan, nil
}

// DeployFunctionAppUsingZipFileFlexConsumption deploys to a Flex Consumption function app
// using pre-fetched properties.
func (cli *AzureClient) DeployFunctionAppUsingZipFileFlexConsumption(
	ctx context.Context,
	subscriptionId string,
	props *AzCliFunctionAppProperties,
	appName string,
	deployZipFile io.ReadSeeker,
	remoteBuild bool,
) (*string, error) {
	hostName, err := functionAppRepositoryHost(props, appName)
	if err != nil {
		return nil, err
	}

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

// DeployFunctionAppUsingZipFileRegular deploys to a regular (non-Flex Consumption) function app
// using pre-fetched properties.
func (cli *AzureClient) DeployFunctionAppUsingZipFileRegular(
	ctx context.Context,
	subscriptionId string,
	props *AzCliFunctionAppProperties,
	appName string,
	deployZipFile io.ReadSeeker,
) (*string, error) {
	hostName, err := functionAppRepositoryHost(props, appName)
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

	return to.Ptr(response.StatusText), nil
}

// functionAppRepositoryHost finds the SCM host name from function app properties.
func functionAppRepositoryHost(props *AzCliFunctionAppProperties, appName string) (string, error) {
	for _, item := range props.HostNameSslStates {
		if *item.HostType == armappservice.HostTypeRepository {
			return *item.Name, nil
		}
	}
	return "", fmt.Errorf("failed to find host name for function app %s", appName)
}
