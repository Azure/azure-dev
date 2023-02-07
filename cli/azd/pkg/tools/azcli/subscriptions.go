package azcli

import (
	"context"
	"fmt"
	"sort"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	azdinternal "github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

// SubscriptionsService allows querying of subscriptions and tenants.
type SubscriptionsService struct {
	credentialProvider auth.MultiTenantCredentialProvider
	userAgent          string
	httpClient         httputil.HttpClient
}

func NewSubscriptionsService(
	credentialProvider auth.MultiTenantCredentialProvider,
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
	cred, err := ss.credentialProvider.GetTokenCredential(ctx, tenantId)
	if err != nil {
		return nil, err
	}

	client, err := armsubscriptions.NewClient(cred, options)
	if err != nil {
		return nil, fmt.Errorf("creating subscriptions client: %w", err)
	}

	return client, nil
}

func (ss *SubscriptionsService) createTenantsClient(ctx context.Context) (*armsubscriptions.TenantsClient, error) {
	options := clientOptionsBuilder(ss.httpClient, ss.userAgent).BuildArmClientOptions()
	// Use default home tenant, since tenants itself can be listed across tenants
	cred, err := ss.credentialProvider.GetTokenCredential(ctx, "")
	if err != nil {
		return nil, err
	}
	client, err := armsubscriptions.NewTenantsClient(cred, options)
	if err != nil {
		return nil, fmt.Errorf("creating tenants client: %w", err)
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

func (s *SubscriptionsService) ListTenants(ctx context.Context) ([]armsubscriptions.TenantIDDescription, error) {
	client, err := s.createTenantsClient(ctx)
	if err != nil {
		return nil, err
	}

	tenants := []armsubscriptions.TenantIDDescription{}
	pager := client.NewListPager(nil)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed getting next page of tenants: %w", err)
		}

		for _, tenant := range page.TenantListResult.Value {
			if tenant != nil {
				tenants = append(tenants, *tenant)
			}
		}
	}

	sort.Slice(tenants, func(i, j int) bool {
		return *tenants[i].DisplayName < *tenants[j].DisplayName
	})

	return tenants, nil
}
