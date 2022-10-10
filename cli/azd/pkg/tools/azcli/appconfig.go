package azcli

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appconfiguration/armappconfiguration"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/identity"
)

type AzCliAppConfig struct {
	Id         string `json:"id"`
	Name       string `json:"name"`
	Location   string `json:"location"`
	Properties struct {
		EnablePurgeProtection bool `json:"enablePurgeProtection"`
	} `json:"properties"`
}

type AzCliAppConfigSecret struct {
	Id    string `json:"id"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (cli *azCli) GetAppConfig(ctx context.Context, subscriptionId string, resourceGroupName string, configName string) (*AzCliAppConfig, error) {
	client, err := cli.createAppConfigClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	config, err := client.Get(ctx, resourceGroupName, configName, nil)
	if err != nil {
		return nil, fmt.Errorf("getting app configuration: %w", err)
	}

	return &AzCliAppConfig{
		Id:       *config.ID,
		Name:     *config.Name,
		Location: *config.Location,
		Properties: struct {
			EnablePurgeProtection bool "json:\"enablePurgeProtection\""
		}{
			EnablePurgeProtection: convert.ToValueWithDefault(config.Properties.EnablePurgeProtection, false),
		},
	}, nil
}

func (cli *azCli) PurgeAppConfig(ctx context.Context, subscriptionId string, configName string, location string) error {
	client, err := cli.createAppConfigClient(ctx, subscriptionId)
	if err != nil {
		return err
	}

	poller, err := client.BeginPurgeDeleted(ctx, configName, location, nil)
	if err != nil {
		return fmt.Errorf("starting purging app configuration: %w", err)
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("purging app configuration: %w", err)
	}

	return nil
}

// Creates a AppConfig client for ARM control plane operations
func (cli *azCli) createAppConfigClient(ctx context.Context, subscriptionId string) (*armappconfiguration.ConfigurationStoresClient, error) {
	cred, err := identity.GetCredentials(ctx)
	if err != nil {
		return nil, err
	}

	options := cli.createArmClientOptions(ctx)
	client, err := armappconfiguration.NewConfigurationStoresClient(subscriptionId, cred, options)
	if err != nil {
		return nil, fmt.Errorf("creating Resource client: %w", err)
	}

	return client, nil
}
