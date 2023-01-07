package azcli

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appconfiguration/armappconfiguration"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
)

type AzCliAppConfig struct {
	Id         string `json:"id"`
	Name       string `json:"name"`
	Location   string `json:"location"`
	Properties struct {
		EnablePurgeProtection bool `json:"enablePurgeProtection"`
	} `json:"properties"`
}

func (cli *azCli) GetAppConfig(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	configName string,
) (*AzCliAppConfig, error) {
	appConfigStoresClient, err := cli.createAppConfigClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	config, err := appConfigStoresClient.Get(ctx, resourceGroupName, configName, nil)
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
	appConfigStoresClient, err := cli.createAppConfigClient(ctx, subscriptionId)
	if err != nil {
		return err
	}

	poller, err := appConfigStoresClient.BeginPurgeDeleted(ctx, location, configName, nil)
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
func (cli *azCli) createAppConfigClient(
	ctx context.Context,
	subscriptionId string,
) (*armappconfiguration.ConfigurationStoresClient, error) {
	options := cli.createDefaultClientOptionsBuilder(ctx).BuildArmClientOptions()
	appConfigStoresClient, err := armappconfiguration.NewConfigurationStoresClient(subscriptionId, cli.credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating Resource client: %w", err)
	}

	return appConfigStoresClient, nil
}
