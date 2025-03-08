package azure

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
)

type AzureClient struct {
	credential azcore.TokenCredential
}

func NewAzureClient(credential azcore.TokenCredential) *AzureClient {
	return &AzureClient{
		credential: credential,
	}
}

func (c *AzureClient) ListLocation(ctx context.Context, subscriptionId string) ([]*armsubscriptions.Location, error) {
	client, err := createSubscriptionsClient(subscriptionId, c.credential)
	if err != nil {
		return nil, err
	}

	pager := client.NewListLocationsPager(subscriptionId, nil)

	var locations []*armsubscriptions.Location
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		locations = append(locations, page.Value...)
	}

	return locations, nil
}

func createSubscriptionsClient(subscriptionId string, credentail azcore.TokenCredential) (*armsubscriptions.Client, error) {
	client, err := armsubscriptions.NewClient(credentail, nil)
	if err != nil {
		return nil, err
	}

	return client, nil
}
