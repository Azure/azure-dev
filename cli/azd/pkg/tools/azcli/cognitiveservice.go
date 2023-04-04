package azcli

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
)

type AzCliCongnitiveService struct {
	Id       string `json:"id"`
	Name     string `json:"name"`
	Location string `json:"location"`
}

func (cli *azCli) GetCognitiveService(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	cogServiceName string,
) (*AzCliCongnitiveService, error) {
	cogServiceClient, err := cli.createCognitiveServiceClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	cogService, err := cogServiceClient.Get(ctx, resourceGroupName, cogServiceName, nil)
	if err != nil {
		return nil, fmt.Errorf("getting cognitive services: %w", err)
	}

	return &AzCliCongnitiveService{
		Id:       *cogService.ID,
		Name:     *cogService.Name,
		Location: *cogService.Location,
	}, nil
}

func (cli *azCli) PurgeCognitiveService(ctx context.Context, subscriptionId string, resourceGroupName string, cogServiceName string, location string) error {
	cogServiceClient, err := cli.createCognitiveServiceDeletedClient(ctx, subscriptionId)
	if err != nil {
		return err
	}

	poller, err := cogServiceClient.BeginPurge(ctx, location, resourceGroupName, cogServiceName, nil)
	if err != nil {
		return fmt.Errorf("starting purging cognitive services: %w", err)
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("purging cognitive services: %w", err)
	}

	return nil
}

// Creates a APIM soft-deleted service client for ARM control plane operations
func (cli *azCli) createCognitiveServiceDeletedClient(
	ctx context.Context,
	subscriptionId string,
) (*armcognitiveservices.DeletedAccountsClient, error) {
	credential, err := cli.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	options := cli.createDefaultClientOptionsBuilder(ctx).BuildArmClientOptions()
	cogServiceClient, err := armcognitiveservices.NewDeletedAccountsClient(subscriptionId, credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating Resource client: %w", err)
	}

	return cogServiceClient, nil
}

// Creates a CognitiveService client for ARM control plane operations
func (cli *azCli) createCognitiveServiceClient(
	ctx context.Context,
	subscriptionId string,
) (*armcognitiveservices.AccountsClient, error) {
	credential, err := cli.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	options := cli.createDefaultClientOptionsBuilder(ctx).BuildArmClientOptions()
	cogServiceClient, err := armcognitiveservices.NewAccountsClient(subscriptionId, credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating Resource client: %w", err)
	}

	return cogServiceClient, nil
}
