package azcli

import (
	"context"
	"fmt"
	"log"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/subscription/armsubscription"
	"github.com/azure/azure-dev/cli/azd/pkg/identity"
)

func (cli *azCli) ListAccounts(ctx context.Context, defaultSubscriptionId string) ([]AzCliSubscriptionInfo, error) {
	client, err := cli.createSubscriptionsClient(ctx)
	if err != nil {
		return nil, err
	}

	subscriptions := []AzCliSubscriptionInfo{}
	pager := client.NewListPager(nil)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed getting next page of subscriptions: %w", err)
		}

		// TODO: How to determine default subscription id without az cli dependency?
		for _, subscription := range page.ListResult.Value {
			subscriptions = append(subscriptions, AzCliSubscriptionInfo{
				Id:        *subscription.SubscriptionID,
				Name:      *subscription.DisplayName,
				IsDefault: defaultSubscriptionId == *subscription.SubscriptionID,
			})
		}
	}

	return subscriptions, nil
}

func (cli *azCli) GetSubscriptionTenant(ctx context.Context, subscriptionId string) (string, error) {
	client, err := cli.createSubscriptionsClient(ctx)
	if err != nil {
		return "", err
	}

	subscription, err := client.Get(ctx, subscriptionId, nil)
	if err != nil {
		return "", fmt.Errorf("failed getting subscription for '%s'", subscriptionId)
	}

	// TODO: GO SDK missing Tenant ID from response
	//return subscription.TenantID, nil
	log.Println(subscription.ID)
	return "TENANT_ID", nil
}

func (cli *azCli) ListAccountLocations(ctx context.Context, subscriptionId string) ([]AzCliLocation, error) {
	client, err := cli.createSubscriptionsClient(ctx)
	if err != nil {
		return nil, err
	}

	locations := []AzCliLocation{}
	pager := client.NewListLocationsPager(subscriptionId, nil)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed getting next page of locations: %w", err)
		}

		// TODO: Previous implementation had a filter to only return where regionType == 'Physical'
		// This prop is not currently available on in the Go SDK
		for _, location := range page.LocationListResult.Value {
			locations = append(locations, AzCliLocation{
				Name:                *location.Name,
				DisplayName:         *location.DisplayName,
				RegionalDisplayName: *location.DisplayName,
			})
		}
	}

	return locations, nil
}

func (cli *azCli) createSubscriptionsClient(ctx context.Context) (*armsubscription.SubscriptionsClient, error) {
	cred, err := identity.GetCredentials(ctx)
	if err != nil {
		return nil, err
	}

	options := cli.createArmClientOptions(ctx)
	client, err := armsubscription.NewSubscriptionsClient(cred, options)
	if err != nil {
		return nil, fmt.Errorf("creating Subscriptions client: %w", err)
	}

	return client, nil
}
