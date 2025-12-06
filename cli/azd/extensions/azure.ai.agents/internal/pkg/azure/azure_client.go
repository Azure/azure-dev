// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"context"
	"slices"
	"strings"

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

func (c *AzureClient) ListLocations(ctx context.Context, subscriptionId string) ([]*armsubscriptions.Location, error) {
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

	slices.SortFunc(locations, func(a, b *armsubscriptions.Location) int {
		return strings.Compare(*a.Name, *b.Name)
	})

	return locations, nil
}

func createSubscriptionsClient(subscriptionId string, credential azcore.TokenCredential) (*armsubscriptions.Client, error) {
	client, err := armsubscriptions.NewClient(credential, NewArmClientOptions())
	if err != nil {
		return nil, err
	}

	return client, nil
}
