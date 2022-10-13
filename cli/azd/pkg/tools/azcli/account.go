package azcli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/azure/azure-dev/cli/azd/pkg/identity"
)

type AzCliSubscriptionInfo struct {
	Name      string `json:"name"`
	Id        string `json:"id"`
	TenantId  string `json:"tenantId"`
	IsDefault bool   `json:"isDefault"`
}

type AzCliLocation struct {
	// The human friendly name of the location (e.g. "West US 2")
	DisplayName string `json:"displayName"`
	// The name of the location (e.g. "westus2")
	Name string `json:"name"`
	// The human friendly name of the location, prefixed with a
	// region name (e.g "(US) West US 2")
	RegionalDisplayName string `json:"regionalDisplayName"`
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

		for _, subscription := range page.SubscriptionListResult.Value {
			subscriptions = append(subscriptions, AzCliSubscriptionInfo{
				Id:       *subscription.SubscriptionID,
				Name:     *subscription.DisplayName,
				TenantId: *subscription.TenantID,
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
		Id:       *subscription.SubscriptionID,
		Name:     *subscription.DisplayName,
		TenantId: *subscription.TenantID,
	}, nil
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

		for _, location := range page.LocationListResult.Value {
			// Ignore non-physical locations
			if *location.Metadata.RegionType != "Physical" {
				continue
			}

			locations = append(locations, AzCliLocation{
				Name:                *location.Name,
				DisplayName:         *location.DisplayName,
				RegionalDisplayName: *location.RegionalDisplayName,
			})
		}
	}

	sort.Slice(locations, func(i, j int) bool {
		return locations[i].RegionalDisplayName < locations[j].RegionalDisplayName
	})

	return locations, nil
}

func (cli *azCli) createSubscriptionsClient(ctx context.Context) (*armsubscriptions.Client, error) {
	cred, err := identity.GetCredentials(ctx)
	if err != nil {
		return nil, err
	}

	// Uses latest api version of subscriptions api to get additional properties
	options := cli.createArmClientOptions(ctx)
	client, err := armsubscriptions.NewClient(cred, options)
	if err != nil {
		return nil, fmt.Errorf("creating Subscriptions client: %w", err)
	}

	return client, nil
}
