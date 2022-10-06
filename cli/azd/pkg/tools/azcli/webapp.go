package azcli

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/identity"
)

type AzCliAppServiceProperties struct {
	HostNames []string `json:"hostNames"`
}

func (cli *azCli) GetAppServiceProperties(ctx context.Context, subscriptionId string, resourceGroup string, appName string) (*AzCliAppServiceProperties, error) {
	client, err := cli.createWebAppsClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	var rawResponse *http.Response
	ctx = runtime.WithCaptureResponse(ctx, &rawResponse)

	_, err = client.Get(ctx, resourceGroup, appName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving webapp properties: %w", err)
	}

	webApp, err := readRawResponse[CustomSite](rawResponse)
	if err != nil {
		return nil, err
	}

	return &AzCliAppServiceProperties{
		HostNames: webApp.Properties.HostNames,
	}, nil
}

func (cli *azCli) DeployAppServiceZip(ctx context.Context, subscriptionId string, resourceGroup string, appName string, deployZipPath string) (string, error) {
	res, err := cli.runAzCommand(ctx, "webapp", "deployment", "source", "config-zip", "--subscription", subscriptionId, "--resource-group", resourceGroup, "--name", appName, "--src", deployZipPath, "--timeout", "3600", "--output", "json")
	if isNotLoggedInMessage(res.Stderr) {
		return "", ErrAzCliNotLoggedIn
	} else if err != nil {
		return "", fmt.Errorf("failed running az deployment source config-zip: %s: %w", res.String(), err)
	}

	return res.Stdout, nil
}

func (cli *azCli) createWebAppsClient(ctx context.Context, subscriptionId string) (*armappservice.WebAppsClient, error) {
	cred, err := identity.GetCredentials(ctx)
	if err != nil {
		return nil, err
	}

	options := cli.createArmClientOptions(ctx, convert.RefOf("2022-03-01"))
	client, err := armappservice.NewWebAppsClient(subscriptionId, cred, options)
	if err != nil {
		return nil, fmt.Errorf("creating WebApps client: %w", err)
	}

	return client, nil
}

type CustomSite struct {
	Id         string               `json:"id"`
	Name       string               `json:"name"`
	Properties CustomSiteProperties `json:"properties"`
}

type CustomSiteProperties struct {
	DefaultHostName string   `json:"defaultHostName"`
	HostNames       []string `json:"hostNames"`
}
