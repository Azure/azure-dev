package azapi

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/apimanagement/armapimanagement"
)

type AzCliApim struct {
	Id       string `json:"id"`
	Name     string `json:"name"`
	Location string `json:"location"`
}

func (cli *AzureClient) GetApim(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	apimName string,
) (*AzCliApim, error) {
	apimClient, err := cli.createApimClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	apim, err := apimClient.Get(ctx, resourceGroupName, apimName, nil)
	if err != nil {
		return nil, fmt.Errorf("getting api management service: %w", err)
	}

	return &AzCliApim{
		Id:       *apim.ID,
		Name:     *apim.Name,
		Location: *apim.Location,
	}, nil
}

func (cli *AzureClient) PurgeApim(ctx context.Context, subscriptionId string, apimName string, location string) error {
	apimClient, err := cli.createApimDeletedClient(ctx, subscriptionId)

	if err != nil {
		return err
	}

	poller, err := apimClient.BeginPurge(ctx, apimName, location, nil)
	if err != nil {
		return fmt.Errorf("starting purging api management service: %w", err)
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("purging api management service: %w", err)
	}

	return nil
}

// Creates a APIM soft-deleted service client for ARM control plane operations
func (cli *AzureClient) createApimDeletedClient(
	ctx context.Context,
	subscriptionId string,
) (*armapimanagement.DeletedServicesClient, error) {
	credential, err := cli.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	apimClient, err := armapimanagement.NewDeletedServicesClient(subscriptionId, credential, cli.armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating Resource client: %w", err)
	}

	return apimClient, nil
}

// Creates a APIM service client for ARM control plane operations
func (cli *AzureClient) createApimClient(
	ctx context.Context,
	subscriptionId string,
) (*armapimanagement.ServiceClient, error) {
	credential, err := cli.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	apimClient, err := armapimanagement.NewServiceClient(subscriptionId, credential, cli.armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating Resource client: %w", err)
	}

	return apimClient, nil
}
