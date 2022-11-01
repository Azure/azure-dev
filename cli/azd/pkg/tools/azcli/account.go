package azcli

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
)

var (
	isNotLoggedInMessageRegex = regexp.MustCompile(
		`(ERROR: No subscription found)|(Please run ('|")az login('|") to (setup account|access your accounts)\.)+`,
	)
	isRefreshTokenExpiredMessageRegex = regexp.MustCompile(`AADSTS(70043|700082)`)
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

// AzCliAccessToken represents the value returned by `az account get-access-token`
type AzCliAccessToken struct {
	AccessToken string
	ExpiresOn   *time.Time
}

func (cli *azCli) ListAccounts(ctx context.Context) ([]*AzCliSubscriptionInfo, error) {
	client, err := cli.createSubscriptionsClient(ctx)
	if err != nil {
		return nil, err
	}

	subscriptions := []*AzCliSubscriptionInfo{}
	pager := client.NewListPager(nil)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed getting next page of subscriptions: %w", err)
		}

		for _, subscription := range page.SubscriptionListResult.Value {
			subscriptions = append(subscriptions,
				&AzCliSubscriptionInfo{
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
	options := cli.createDefaultClientOptionsBuilder(ctx).BuildArmClientOptions()
	client, err := armsubscriptions.NewClient(cli.credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating Subscriptions client: %w", err)
	}

	return client, nil
}

func (cli *azCli) GetAccessToken(ctx context.Context) (*AzCliAccessToken, error) {
	token, err := cli.credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{
			fmt.Sprintf("%s/.default", cloud.AzurePublic.Services[cloud.ResourceManager].Audience),
		},
	})

	if err != nil {
		if isNotLoggedInMessage(err.Error()) {
			return nil, ErrAzCliNotLoggedIn
		} else if isRefreshTokenExpiredMessage(err.Error()) {
			return nil, ErrAzCliRefreshTokenExpired
		}

		return nil, fmt.Errorf("failed retrieving access token: %w", err)
	}

	return &AzCliAccessToken{
		AccessToken: token.Token,
		ExpiresOn:   &token.ExpiresOn,
	}, nil
}

func isNotLoggedInMessage(s string) bool {
	return isNotLoggedInMessageRegex.MatchString(s)
}

func isRefreshTokenExpiredMessage(s string) bool {
	return isRefreshTokenExpiredMessageRegex.MatchString(s)
}
