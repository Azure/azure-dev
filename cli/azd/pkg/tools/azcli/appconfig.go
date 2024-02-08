package azcli

import (
	"context"
	"fmt"

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
	config, err := cli.appConfigStoresClient.Get(ctx, resourceGroupName, configName, nil)
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
	poller, err := cli.appConfigStoresClient.BeginPurgeDeleted(ctx, location, configName, nil)
	if err != nil {
		return fmt.Errorf("starting purging app configuration: %w", err)
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("purging app configuration: %w", err)
	}

	return nil
}
