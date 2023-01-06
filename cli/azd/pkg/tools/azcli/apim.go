package azcli

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/apimanagement/armapimanagement"
)

type AzCliAPIM struct {
	Id       string `json:"id"`
	Name     string `json:"name"`
	Location string `json:"location"`
}

func (cli *azCli) GetAPIM(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	apimName string,
) (*AzCliAPIM, error) {
	apimClient, err := cli.createAPIMClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	apim, err := apimClient.Get(ctx, resourceGroupName, apimName, nil)
	if err != nil {
		return nil, fmt.Errorf("getting api management: %w", err)
	}

	return &AzCliAPIM{
		Id:       *apim.ID,
		Name:     *apim.Name,
		Location: *apim.Location,
	}, nil
}

func (cli *azCli) PurgeAPIM(ctx context.Context, subscriptionId string, apimName string, location string) error {
	apimClient, err := cli.createAPIMDeletedClient(ctx, subscriptionId)

	if err != nil {
		return err
	}

	poller, err := apimClient.BeginPurge(ctx, apimName, location, nil)
	if err != nil {
		return fmt.Errorf("starting purging api management: %w", err)
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("purging api management: %w", err)
	}

	return nil
}

// Creates a APIM soft-deleted service client for ARM control plane operations
func (cli *azCli) createAPIMDeletedClient(
	ctx context.Context,
	subscriptionId string,
) (*armapimanagement.DeletedServicesClient, error) {
	options := cli.createDefaultClientOptionsBuilder(ctx).BuildArmClientOptions()
	apimClient, err := armapimanagement.NewDeletedServicesClient(subscriptionId, cli.credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating Resource client: %w", err)
	}

	return apimClient, nil
}

// Creates a APIM service client for ARM control plane operations
func (cli *azCli) createAPIMClient(
	ctx context.Context,
	subscriptionId string,
) (*armapimanagement.ServiceClient, error) {
	options := cli.createDefaultClientOptionsBuilder(ctx).BuildArmClientOptions()
	apimClient, err := armapimanagement.NewServiceClient(subscriptionId, cli.credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating Resource client: %w", err)
	}

	return apimClient, nil
}
