package azapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v2"
)

type AzCliStaticWebAppProperties struct {
	DefaultHostname string
}

type AzCliStaticWebAppEnvironmentProperties struct {
	Hostname string
	Status   string
}

func (cli *AzureClient) GetStaticWebAppProperties(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	appName string,
) (*AzCliStaticWebAppProperties, error) {
	client, err := cli.createStaticSitesClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	staticSite, err := client.GetStaticSite(ctx, resourceGroup, appName, nil)
	if err != nil {
		return nil, fmt.Errorf("retrieving static site '%s': %w", appName, err)
	}

	return &AzCliStaticWebAppProperties{
		DefaultHostname: *staticSite.Properties.DefaultHostname,
	}, nil
}

func (cli *AzureClient) GetStaticWebAppEnvironmentProperties(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	appName string,
	environmentName string,
) (*AzCliStaticWebAppEnvironmentProperties, error) {
	client, err := cli.createStaticSitesClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	build, err := client.GetStaticSiteBuild(ctx, resourceGroup, appName, environmentName, nil)
	if err != nil {
		return nil, fmt.Errorf("retrieving static site environment '%s': %w", environmentName, err)
	}

	return &AzCliStaticWebAppEnvironmentProperties{
		Hostname: *build.Properties.Hostname,
		Status:   string(*build.Properties.Status),
	}, nil
}

func (cli *AzureClient) GetStaticWebAppApiKey(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	appName string,
) (*string, error) {
	client, err := cli.createStaticSitesClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	secretsResponse, err := client.ListStaticSiteSecrets(ctx, resourceGroup, appName, nil)
	if err != nil {
		return nil, fmt.Errorf("retrieving static site secrets for '%s': %w", appName, err)
	}

	apiKey, ok := secretsResponse.Properties["apiKey"]
	if !ok {
		return nil, errors.New("cannot find property 'apiKey'")
	}

	return apiKey, nil
}

func (cli *AzureClient) createStaticSitesClient(
	ctx context.Context,
	subscriptionId string,
) (*armappservice.StaticSitesClient, error) {
	credential, err := cli.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	client, err := armappservice.NewStaticSitesClient(subscriptionId, credential, cli.armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating Static Sites client: %w", err)
	}

	return client, nil
}
