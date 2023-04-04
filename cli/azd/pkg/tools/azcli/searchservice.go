package azcli

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/search/armsearch"
)

type AzCliSearchService struct {
	Id       string `json:"id"`
	Name     string `json:"name"`
	Location string `json:"location"`
}

func (cli *azCli) GetSearchService(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	searchName string,
) (*AzCliSearchService, error) {
	searchServiceClient, err := cli.createSearchServiceClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	search, err := searchServiceClient.Get(ctx, resourceGroupName, searchName, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("getting search service: %w", err)
	}

	return &AzCliSearchService{
		Id:       *search.ID,
		Name:     *search.Name,
		Location: *search.Location,
	}, nil
}

func (cli *azCli) PurgeSearchService(ctx context.Context, subscriptionId string, searchName string, location string) error {
	searchServiceClient, err := cli.createSearchServiceClient(ctx, subscriptionId)
	if err != nil {
		return err
	}

	response, err := searchServiceClient.Delete(ctx, location, searchName, nil, nil)
	if err != nil {
		return fmt.Errorf("starting purging search service: %w. response: %s", err, response)
	}

	return nil
}

// Creates a SearchService client for ARM control plane operations
func (cli *azCli) createSearchServiceClient(
	ctx context.Context,
	subscriptionId string,
) (*armsearch.ServicesClient, error) {
	credential, err := cli.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	options := cli.createDefaultClientOptionsBuilder(ctx).BuildArmClientOptions()
	searchServiceClient, err := armsearch.NewServicesClient(subscriptionId, credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating Resource client: %w", err)
	}

	return searchServiceClient, nil
}
