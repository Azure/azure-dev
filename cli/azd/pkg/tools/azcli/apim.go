package azcli

import (
	"context"
	"fmt"
)

type AzCliApim struct {
	Id       string `json:"id"`
	Name     string `json:"name"`
	Location string `json:"location"`
}

func (cli *azCli) GetApim(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	apimName string,
) (*AzCliApim, error) {
	apim, err := cli.apimServiceClient.Get(ctx, resourceGroupName, apimName, nil)
	if err != nil {
		return nil, fmt.Errorf("getting api management service: %w", err)
	}

	return &AzCliApim{
		Id:       *apim.ID,
		Name:     *apim.Name,
		Location: *apim.Location,
	}, nil
}

func (cli *azCli) PurgeApim(ctx context.Context, subscriptionId string, apimName string, location string) error {
	poller, err := cli.apimDeletedClient.BeginPurge(ctx, apimName, location, nil)
	if err != nil {
		return fmt.Errorf("starting purging api management service: %w", err)
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("purging api management service: %w", err)
	}

	return nil
}
