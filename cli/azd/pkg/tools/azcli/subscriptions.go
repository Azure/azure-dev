package azcli

import (
	"context"
	"fmt"
	"sort"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	azdinternal "github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

type MultiTenantCredentialProvider interface {
	GetTokenCredential(tenantId string) azcore.TokenCredential
}

type SubscriptionsService struct {
	credentialProvider MultiTenantCredentialProvider
	userAgent          string
	httpClient         httputil.HttpClient
}

func NewSubscriptionsService(
	credentialProvider MultiTenantCredentialProvider,
	httpClient httputil.HttpClient) *SubscriptionsService {
	return &SubscriptionsService{
		userAgent:          azdinternal.MakeUserAgentString(""),
		httpClient:         httpClient,
		credentialProvider: credentialProvider,
	}
}

func (ss *SubscriptionsService) createSubscriptionsClient(
	ctx context.Context, tenantId string) (*armsubscriptions.Client, error) {
	options := clientOptionsBuilder(ss.httpClient, ss.userAgent).BuildArmClientOptions()
	client, err := armsubscriptions.NewClient(ss.credentialProvider.GetTokenCredential(tenantId), options)
	if err != nil {
		return nil, fmt.Errorf("creating Subscriptions client: %w", err)
	}

	return client, nil
}

func (s *SubscriptionsService) ListSubscriptions(ctx context.Context, tenantId string) ([]AzCliSubscriptionInfo, error) {
	client, err := s.createSubscriptionsClient(ctx, tenantId)
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
			subscriptions = append(subscriptions,
				AzCliSubscriptionInfo{
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

func (s *SubscriptionsService) GetSubscription(
	ctx context.Context, subscriptionId string, tenantId string) (*AzCliSubscriptionInfo, error) {
	client, err := s.createSubscriptionsClient(ctx, tenantId)
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

// ListSubscriptionLocations lists physical locations in Azure for the given subscription.
func (s *SubscriptionsService) ListSubscriptionLocations(
	ctx context.Context, subscriptionId string, tenantId string) ([]AzCliLocation, error) {
	client, err := s.createSubscriptionsClient(ctx, tenantId)
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
