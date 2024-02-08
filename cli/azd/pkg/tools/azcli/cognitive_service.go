package azcli

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
)

// GetCognitiveAccount finds the cognitive account within a subscription
func (cli *azCli) GetCognitiveAccount(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	accountName string,
) (armcognitiveservices.Account, error) {
	response, err := cli.cognitiveServicesAccountsClient.Get(ctx, resourceGroupName, accountName, nil)
	if err != nil {
		return armcognitiveservices.Account{}, err
	}

	return response.Account, nil
}

// PurgeCognitiveAccount starts purge operation and wait until it is completed.
func (cli *azCli) PurgeCognitiveAccount(
	ctx context.Context,
	subscriptionId,
	location,
	resourceGroup,
	accountName string,
) error {

	poller, err := cli.cognitiveServicesDeletedAccountsClient.BeginPurge(ctx, location, resourceGroup, accountName, nil)
	if err != nil {
		return fmt.Errorf("starting purging cognitive account: %w", err)
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("purging cognitive account: %w", err)
	}

	return nil
}
