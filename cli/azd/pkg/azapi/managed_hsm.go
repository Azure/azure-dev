package azapi

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
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

func (cli *AzureClient) GetManagedHSM(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	hsmName string,
) (*AzCliManagedHSM, error) {
	client, err := cli.createManagedHSMClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	managedHSM, err := client.Get(ctx, resourceGroupName, hsmName, nil)
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

func (cli *AzureClient) PurgeManagedHSM(ctx context.Context, subscriptionId string, hsmName string, location string) error {
	client, err := cli.createManagedHSMClient(ctx, subscriptionId)
	if err != nil {
		return err
	}

	poller, err := client.BeginPurgeDeleted(ctx, hsmName, location, nil)
	if err != nil {
		return fmt.Errorf("starting purging managed hsm: %w", err)
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("purging managed hsm: %w", err)
	}

	return nil
}

// Creates a Managed HSM client for ARM control plane operations
func (cli *AzureClient) createManagedHSMClient(
	ctx context.Context,
	subscriptionId string,
) (*armkeyvault.ManagedHsmsClient, error) {
	credential, err := cli.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	client, err := armkeyvault.NewManagedHsmsClient(subscriptionId, credential, cli.armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating Resource client: %w", err)
	}

	return client, nil
}
