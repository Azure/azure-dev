// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package account

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/compare"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
)

// SubscriptionsService allows querying of subscriptions and tenants.
type SubscriptionsService struct {
	credentialProvider auth.MultiTenantCredentialProvider
	armClientOptions   *arm.ClientOptions
}

func NewSubscriptionsService(
	credentialProvider auth.MultiTenantCredentialProvider,
	armClientOptions *arm.ClientOptions,
) *SubscriptionsService {
	return &SubscriptionsService{
		credentialProvider: credentialProvider,
		armClientOptions:   armClientOptions,
	}
}

func (ss *SubscriptionsService) createSubscriptionsClient(
	ctx context.Context, tenantId string) (*armsubscriptions.Client, error) {
	cred, err := ss.credentialProvider.GetTokenCredential(ctx, tenantId)
	if err != nil {
		return nil, err
	}

	client, err := armsubscriptions.NewClient(cred, ss.armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating subscriptions client: %w", err)
	}

	return client, nil
}

func (ss *SubscriptionsService) createTenantsClient(ctx context.Context) (*armsubscriptions.TenantsClient, error) {
	// Use default home tenant, since tenants itself can be listed across tenants
	cred, err := ss.credentialProvider.GetTokenCredential(ctx, "")
	if err != nil {
		return nil, err
	}
	client, err := armsubscriptions.NewTenantsClient(cred, ss.armClientOptions)
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

// LocationFilterOptions provides filtering options for location queries
type LocationFilterOptions struct {
	// ResourceTypes filters by specific resource types (e.g., "Microsoft.App/containerApps")
	ResourceTypes []string
}

// ListSubscriptionLocationsWithFilter lists physical locations with optional resource type filtering.
// When ResourceTypes are provided, only locations that support ALL specified resource types are returned.
func (s *SubscriptionsService) ListSubscriptionLocationsWithFilter(
	ctx context.Context,
	subscriptionId string,
	tenantId string,
	options *LocationFilterOptions,
) ([]Location, error) {
	// Get all physical locations first
	allLocations, err := s.ListSubscriptionLocations(ctx, subscriptionId, tenantId)
	if err != nil {
		return nil, err
	}

	// If no filtering options or no resource types specified, return all locations
	if options == nil || len(options.ResourceTypes) == 0 {
		return allLocations, nil
	}

	// Check resource type availability for each location
	filteredLocations := []Location{}
	for _, location := range allLocations {
		supported, err := s.checkResourceTypesAvailability(
			ctx, subscriptionId, tenantId, location.Name, options.ResourceTypes)
		if err != nil {
			// Log error but continue with other locations to provide best-effort filtering.
			// If all locations fail, an empty list will be returned, prompting the user to check permissions.
			fmt.Printf("warning: failed to check resource availability for location %s: %v\n", location.Name, err)
			continue
		}

		if supported {
			filteredLocations = append(filteredLocations, location)
		}
	}

	return filteredLocations, nil
}

// checkResourceTypesAvailability checks if all specified resource types are available in the given location.
func (s *SubscriptionsService) checkResourceTypesAvailability(
	ctx context.Context,
	subscriptionId string,
	tenantId string,
	locationName string,
	resourceTypes []string,
) (bool, error) {
	if len(resourceTypes) == 0 {
		return true, nil
	}

	// Group resource types by provider namespace
	providerResourceTypes := make(map[string][]string)
	for _, resourceType := range resourceTypes {
		parts := strings.SplitN(resourceType, "/", 2)
		if len(parts) != 2 {
			// Skip invalid resource types (should be in format "Provider/Type")
			// This could indicate a template parsing issue, so log for debugging
			fmt.Printf(
				"warning: skipping invalid resource type format '%s' (expected 'Provider/Type')\n",
				resourceType)
			continue
		}
		providerNamespace := parts[0]
		typeName := parts[1]
		providerResourceTypes[providerNamespace] = append(providerResourceTypes[providerNamespace], typeName)
	}

	// Create providers client
	cred, err := s.credentialProvider.GetTokenCredential(ctx, tenantId)
	if err != nil {
		return false, fmt.Errorf("getting credential: %w", err)
	}

	providersClient, err := armresources.NewProvidersClient(subscriptionId, cred, s.armClientOptions)
	if err != nil {
		return false, fmt.Errorf("creating providers client: %w", err)
	}

	// Check each provider's resource types
	for providerNamespace, typeNames := range providerResourceTypes {
		provider, err := providersClient.Get(ctx, providerNamespace, nil)
		if err != nil {
			return false, fmt.Errorf("getting provider %s: %w", providerNamespace, err)
		}

		// Check if provider is registered
		if provider.RegistrationState == nil || *provider.RegistrationState != "Registered" {
			return false, nil
		}

		// Check each resource type
		for _, typeName := range typeNames {
			found := false
			if provider.ResourceTypes != nil {
				for _, rt := range provider.ResourceTypes {
					if rt.ResourceType != nil && *rt.ResourceType == typeName {
						// Check if this location is supported
						if rt.Locations != nil {
							locationSupported := false
							for _, loc := range rt.Locations {
								if loc != nil && strings.EqualFold(*loc, locationName) {
									locationSupported = true
									break
								}
							}
							if !locationSupported {
								return false, nil
							}
						}
						found = true
						break
					}
				}
			}

			if !found {
				return false, nil
			}
		}
	}

	return true, nil
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
