package azure

import (
	"context"
	"fmt"
	"sort"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/azure/azure-dev/cli/sdk/azdcore/common/compare"
	"github.com/azure/azure-dev/cli/sdk/azdcore/common/convert"
)

// SubscriptionsService allows querying of subscriptions and tenants.
type SubscriptionsService struct {
	credential       azcore.TokenCredential
	armClientOptions *arm.ClientOptions
}

func NewSubscriptionsService(
	credential azcore.TokenCredential,
	armClientOptions *arm.ClientOptions,
) *SubscriptionsService {
	return &SubscriptionsService{
		credential:       credential,
		armClientOptions: armClientOptions,
	}
}

func (ss *SubscriptionsService) createSubscriptionsClient(
	ctx context.Context,
	tenantId string,
) (*armsubscriptions.Client, error) {

	client, err := armsubscriptions.NewClient(ss.credential, ss.armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating subscriptions client: %w", err)
	}

	return client, nil
}

func (ss *SubscriptionsService) createTenantsClient(ctx context.Context) (*armsubscriptions.TenantsClient, error) {
	// Use default home tenant, since tenants itself can be listed across tenants
	client, err := armsubscriptions.NewTenantsClient(ss.credential, ss.armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating tenants client: %w", err)
	}

	return client, nil
}

func (s *SubscriptionsService) ListSubscriptions(
	ctx context.Context,
	tenantId string,
) ([]*armsubscriptions.Subscription, error) {
	client, err := s.createSubscriptionsClient(ctx, tenantId)
	if err != nil {
		return nil, err
	}

	subscriptions := []*armsubscriptions.Subscription{}
	pager := client.NewListPager(nil)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed getting next page of subscriptions: %w", err)
		}

		subscriptions = append(subscriptions, page.SubscriptionListResult.Value...)
	}

	sort.Slice(subscriptions, func(i, j int) bool {
		return *subscriptions[i].DisplayName < *subscriptions[j].DisplayName
	})

	return subscriptions, nil
}

func (s *SubscriptionsService) GetSubscription(
	ctx context.Context, subscriptionId string, tenantId string) (*armsubscriptions.Subscription, error) {
	client, err := s.createSubscriptionsClient(ctx, tenantId)
	if err != nil {
		return nil, err
	}

	subscription, err := client.Get(ctx, subscriptionId, nil)
	if err != nil {
		return nil, fmt.Errorf("failed getting subscription for '%s'", subscriptionId)
	}

	return &subscription.Subscription, nil
}

// ListSubscriptionLocations lists physical locations in Azure for the given subscription.
func (s *SubscriptionsService) ListSubscriptionLocations(
	ctx context.Context, subscriptionId string, tenantId string) ([]Location, error) {
	client, err := s.createSubscriptionsClient(ctx, tenantId)
	if err != nil {
		return nil, err
	}

	locations := []Location{}
	pager := client.NewListLocationsPager(subscriptionId, nil)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed getting next page of locations: %w", err)
		}

		for _, location := range page.LocationListResult.Value {
			// Only include physical locations
			if *location.Metadata.RegionType == "Physical" &&
				!compare.PtrValueEquals(location.Metadata.PhysicalLocation, "") {
				displayName := convert.ToValueWithDefault(location.DisplayName, *location.Name)
				regionalDisplayName := convert.ToValueWithDefault(location.RegionalDisplayName, displayName)

				locations = append(locations, Location{
					Name:                *location.Name,
					DisplayName:         displayName,
					RegionalDisplayName: regionalDisplayName,
				})
			}
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
			if tenant != nil && tenant.TenantID != nil {
				tenants = append(tenants, *tenant)
			}
		}
	}

	sort.Slice(tenants, func(i, j int) bool {
		return convert.ToValueWithDefault(tenants[i].DisplayName, "") <
			convert.ToValueWithDefault(tenants[j].DisplayName, "")
	})

	return tenants, nil
}
