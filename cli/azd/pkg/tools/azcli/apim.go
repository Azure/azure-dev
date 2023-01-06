package azcli

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/apimanagement/armapimanagement"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
)

type AzCliAPIM struct {
	Id         string `json:"id"`
	Name       string `json:"name"`
	Location   string `json:"location"`
	Properties struct {
		ScheduledPurgeDate string `json:"scheduledPurgeDate"`
	} `json:"properties"`
}

type AzCliAPIMSecret struct {
	Id    string `json:"id"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (cli *azCli) GetAPIM(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	apimName string,
	apimId string,
) (*AzCliAPIM, error) {
	apimClient, err := cli.createAPIMClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	apim, err := apimClient.Get(ctx, resourceGroupName, apimName, apimId, nil)
	if err != nil {
		return nil, fmt.Errorf("getting api management: %w", err)
	}

	return &AzCliAPIM{
		Id:       *apim.ID,
		Name:     *apim.Name,
		Location: *apim.Location,
		Properties: struct {
			ScheduledPurgeDate string "json:\"scheduledPurgeDate\""
		}{
			ScheduledPurgeDate: convert.ToValueWithDefault(apim.Properties.ScheduledPurgeDate, ""),
		},
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

// Creates a APIM client for ARM control plane operations
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

// Creates a APIM client for ARM control plane operations
func (cli *azCli) createAPIMClient(
	ctx context.Context,
	subscriptionId string,
) (*armapimanagement.APIClient, error) {
	options := cli.createDefaultClientOptionsBuilder(ctx).BuildArmClientOptions()
	apimClient, err := armapimanagement.NewAPIClient(subscriptionId, cli.credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating Resource client: %w", err)
	}

	return apimClient, nil
}
