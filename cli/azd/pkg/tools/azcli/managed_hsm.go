package azcli

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/convert"
)

type AzCliManagedHSM struct {
	Id         string `json:"id"`
	Name       string `json:"name"`
	Location   string `json:"location"`
	Properties struct {
		EnableSoftDelete      bool `json:"enableSoftDelete"`
		EnablePurgeProtection bool `json:"enablePurgeProtection"`
	} `json:"properties"`
}

func (cli *azCli) GetManagedHSM(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	hsmName string,
) (*AzCliManagedHSM, error) {
	managedHSM, err := cli.managedHsmsClient.Get(ctx, resourceGroupName, hsmName, nil)
	if err != nil {
		return nil, fmt.Errorf("getting managed hsm: %w", err)
	}

	return &AzCliManagedHSM{
		Id:       *managedHSM.ID,
		Name:     *managedHSM.Name,
		Location: *managedHSM.Location,
		Properties: struct {
			EnableSoftDelete      bool "json:\"enableSoftDelete\""
			EnablePurgeProtection bool "json:\"enablePurgeProtection\""
		}{
			EnableSoftDelete:      convert.ToValueWithDefault(managedHSM.Properties.EnableSoftDelete, false),
			EnablePurgeProtection: convert.ToValueWithDefault(managedHSM.Properties.EnablePurgeProtection, false),
		},
	}, nil
}

func (cli *azCli) PurgeManagedHSM(ctx context.Context, subscriptionId string, hsmName string, location string) error {
	poller, err := cli.managedHsmsClient.BeginPurgeDeleted(ctx, hsmName, location, nil)
	if err != nil {
		return fmt.Errorf("starting purging managed hsm: %w", err)
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("purging managed hsm: %w", err)
	}

	return nil
}
