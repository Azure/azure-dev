package azcli

import (
	"context"
	"errors"
	"fmt"
)

type AzCliStaticWebAppProperties struct {
	DefaultHostname string
}

type AzCliStaticWebAppEnvironmentProperties struct {
	Hostname string
	Status   string
}

func (cli *azCli) GetStaticWebAppProperties(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	appName string,
) (*AzCliStaticWebAppProperties, error) {
	staticSite, err := cli.staticSitesClient.GetStaticSite(ctx, resourceGroup, appName, nil)
	if err != nil {
		return nil, fmt.Errorf("retrieving static site '%s': %w", appName, err)
	}

	return &AzCliStaticWebAppProperties{
		DefaultHostname: *staticSite.Properties.DefaultHostname,
	}, nil
}

func (cli *azCli) GetStaticWebAppEnvironmentProperties(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	appName string,
	environmentName string,
) (*AzCliStaticWebAppEnvironmentProperties, error) {
	build, err := cli.staticSitesClient.GetStaticSiteBuild(ctx, resourceGroup, appName, environmentName, nil)
	if err != nil {
		return nil, fmt.Errorf("retrieving static site environment '%s': %w", environmentName, err)
	}

	return &AzCliStaticWebAppEnvironmentProperties{
		Hostname: *build.Properties.Hostname,
		Status:   string(*build.Properties.Status),
	}, nil
}

func (cli *azCli) GetStaticWebAppApiKey(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	appName string,
) (*string, error) {
	secretsResponse, err := cli.staticSitesClient.ListStaticSiteSecrets(ctx, resourceGroup, appName, nil)
	if err != nil {
		return nil, fmt.Errorf("retrieving static site secrets for '%s': %w", appName, err)
	}

	apiKey, ok := secretsResponse.Properties["apiKey"]
	if !ok {
		return nil, errors.New("cannot find property 'apiKey'")
	}

	return apiKey, nil
}
