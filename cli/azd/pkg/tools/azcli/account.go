package azcli

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/subscription/armsubscription"
	"github.com/azure/azure-dev/cli/azd/pkg/identity"
)

type AzCliSubscriptionInfo struct {
	Name      string `json:"name"`
	Id        string `json:"id"`
	IsDefault bool   `json:"isDefault"`
}

func (cli *azCli) ListAccounts(ctx context.Context) ([]AzCliSubscriptionInfo, error) {
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
				Id:   *subscription.SubscriptionID,
				Name: *subscription.DisplayName,
			})
		}
	}

	sort.Slice(subscriptions, func(i, j int) bool {
		return subscriptions[i].Name < subscriptions[j].Name
	})

	return subscriptions, nil
}

func (cli *azCli) GetDefaultAccount(ctx context.Context) (*AzCliSubscriptionInfo, error) {
	result, err := cli.runAzCommand(
		ctx,
		"account", "show",
		"--output", "json",
	)

	if err != nil {
		return nil, fmt.Errorf("failed getting default account from az cli: %w", err)
	}

	var subscription AzCliSubscriptionInfo
	err = json.Unmarshal([]byte(result.Stdout), &subscription)
	if err != nil {
		return nil, fmt.Errorf("failed unmarshalling result JSON: %w", err)
	}

	return &subscription, nil
}

func (cli *azCli) GetAccount(ctx context.Context, subscriptionId string) (*AzCliSubscriptionInfo, error) {
	client, err := cli.createSubscriptionsClient(ctx)
	if err != nil {
		return nil, err
	}

	subscription, err := client.Get(ctx, subscriptionId, nil)
	if err != nil {
		return nil, fmt.Errorf("failed getting subscription for '%s'", subscriptionId)
	}

	return &AzCliSubscriptionInfo{
		Id:   *subscription.SubscriptionID,
		Name: *subscription.DisplayName,
	}, nil
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

	sort.Slice(locations, func(i, j int) bool {
		return locations[i].DisplayName < locations[j].DisplayName
	})

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
