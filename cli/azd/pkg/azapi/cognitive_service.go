// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
)

// GetCognitiveAccount finds the cognitive account within a subscription
func (cli *AzureClient) GetCognitiveAccount(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	accountName string) (armcognitiveservices.Account, error) {
	client, err := cli.createCognitiveAccountClient(ctx, subscriptionId)
	if err != nil {
		return armcognitiveservices.Account{}, err
	}

	response, err := client.Get(ctx, resourceGroupName, accountName, nil)
	if err != nil {
		return armcognitiveservices.Account{}, err
	}

	return response.Account, nil
}

// PurgeCognitiveAccount starts purge operation and wait until it is completed.
func (cli *AzureClient) PurgeCognitiveAccount(
	ctx context.Context, subscriptionId, location, resourceGroup, accountName string) error {
	client, err := cli.createDeletedCognitiveAccountClient(ctx, subscriptionId)
	if err != nil {
		return err
	}

	poller, err := client.BeginPurge(ctx, location, resourceGroup, accountName, nil)
	if err != nil {
		return fmt.Errorf("starting purging cognitive account: %w", err)
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("purging cognitive account: %w", err)
	}

	return nil
}

func (cli *AzureClient) createCognitiveAccountClient(
	ctx context.Context, subscriptionId string) (*armcognitiveservices.AccountsClient, error) {
	return cli.cognitiveAccountsCache.GetOrCreate(subscriptionId, func() (*armcognitiveservices.AccountsClient, error) {
		credential, err := cli.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
		if err != nil {
			return nil, err
		}

		client, err := armcognitiveservices.NewAccountsClient(subscriptionId, credential, cli.armClientOptions)
		if err != nil {
			return nil, fmt.Errorf("creating Resource client: %w", err)
		}

		return client, nil
	})
}

func (cli *AzureClient) createDeletedCognitiveAccountClient(
	ctx context.Context, subscriptionId string) (*armcognitiveservices.DeletedAccountsClient, error) {
	return cli.deletedCognitiveCache.GetOrCreate(
		subscriptionId,
		func() (*armcognitiveservices.DeletedAccountsClient, error) {
			credential, err := cli.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
			if err != nil {
				return nil, err
			}

			client, err := armcognitiveservices.NewDeletedAccountsClient(subscriptionId, credential, cli.armClientOptions)
			if err != nil {
				return nil, fmt.Errorf("creating Resource client: %w", err)
			}

			return client, nil
		},
	)
}

func (cli *AzureClient) GetAiModels(
	ctx context.Context,
	subscriptionId string,
	location string) ([]*armcognitiveservices.Model, error) {
	client, err := cli.createModelsClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	modelsPager := client.NewListPager(location, nil)
	var models []*armcognitiveservices.Model
	for modelsPager.More() {
		page, err := modelsPager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		models = append(models, page.Value...)
	}

	return models, nil
}

func (cli *AzureClient) createModelsClient(
	ctx context.Context, subscriptionId string) (*armcognitiveservices.ModelsClient, error) {
	return cli.cognitiveModelsCache.GetOrCreate(subscriptionId, func() (*armcognitiveservices.ModelsClient, error) {
		credential, err := cli.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
		if err != nil {
			return nil, err
		}

		client, err := armcognitiveservices.NewModelsClient(subscriptionId, credential, cli.armClientOptions)
		if err != nil {
			return nil, fmt.Errorf("creating Resource client: %w", err)
		}

		return client, nil
	})
}

func (cli *AzureClient) GetAiUsages(
	ctx context.Context,
	subscriptionId string,
	location string) ([]*armcognitiveservices.Usage, error) {
	client, err := cli.createUsagesClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	modelsPager := client.NewListPager(location, nil)
	var models []*armcognitiveservices.Usage
	for modelsPager.More() {
		page, err := modelsPager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		models = append(models, page.Value...)
	}

	return models, nil
}

func (cli *AzureClient) createUsagesClient(
	ctx context.Context, subscriptionId string) (*armcognitiveservices.UsagesClient, error) {
	return cli.cognitiveUsagesCache.GetOrCreate(subscriptionId, func() (*armcognitiveservices.UsagesClient, error) {
		credential, err := cli.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
		if err != nil {
			return nil, err
		}

		client, err := armcognitiveservices.NewUsagesClient(subscriptionId, credential, cli.armClientOptions)
		if err != nil {
			return nil, fmt.Errorf("creating Resource client: %w", err)
		}

		return client, nil
	})
}

// GetResourceSkuLocations retrieves a list of unique locations where a specific resource SKU is available.
// It filters the resource SKUs based on the provided kind, SKU name, tier, and resource type.
//
// Parameters:
//   - ctx: The context for the operation.
//   - subscriptionId: The Azure subscription ID.
//   - kind: The kind of the resource (e.g., "CognitiveServices").
//   - sku: The name of the SKU (e.g., "S1").
//   - tier: The tier of the SKU (e.g., "Standard").
//   - resourceType: The type of the resource (e.g., "Microsoft.CognitiveServices/accounts").
//
// Returns:
//   - A slice of strings containing the unique locations where the specified SKU is available.
//   - An error if the operation fails or no locations are found.
//
// Notes:
//   - The function ensures that the returned list of locations is sorted in a consistent order.
//   - If no locations are found for the specified SKU, an error is returned.
func (cli *AzureClient) GetResourceSkuLocations(
	ctx context.Context,
	subscriptionId string,
	kind, sku, tier, resourceType string) ([]string, error) {
	client, err := cli.createResourcesSkuClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	// The Resource SKUs API returns a list of SKUs, each with a list of locations.
	// However, it is currently returning one entry for each location, instead of one entry with the full list of locations.
	// To avoid any possible duplicates, we will use a map to track unique locations.
	locationsUniqueName := map[string]struct{}{}

	resourceSkusResponse := client.NewListPager(nil)
	for resourceSkusResponse.More() {
		page, err := resourceSkusResponse.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, skuInfo := range page.Value {
			if skuInfo.Kind != nil && *skuInfo.Kind == kind &&
				skuInfo.Name != nil && *skuInfo.Name == sku &&
				skuInfo.Tier != nil && *skuInfo.Tier == tier &&
				skuInfo.ResourceType != nil && *skuInfo.ResourceType == resourceType {
				for _, location := range skuInfo.Locations {
					locationsUniqueName[*location] = struct{}{}
				}
			}
		}
	}
	if len(locationsUniqueName) == 0 {
		return nil, fmt.Errorf("no locations found for sku %s", sku)
	}
	locations := make([]string, 0, len(locationsUniqueName))
	for location := range locationsUniqueName {
		locations = append(locations, strings.ToLower(location))
	}
	// make the output consistent
	slices.Sort(locations)
	return locations, nil
}

func (cli *AzureClient) createResourcesSkuClient(
	ctx context.Context, subscriptionId string) (*armcognitiveservices.ResourceSKUsClient, error) {
	return cli.cognitiveResourceSkusCache.GetOrCreate(
		subscriptionId,
		func() (*armcognitiveservices.ResourceSKUsClient, error) {
			credential, err := cli.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
			if err != nil {
				return nil, err
			}

			client, err := armcognitiveservices.NewResourceSKUsClient(subscriptionId, credential, cli.armClientOptions)
			if err != nil {
				return nil, fmt.Errorf("creating Resource client: %w", err)
			}

			return client, nil
		},
	)
}
