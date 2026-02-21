// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
)

// ResourceTypeLocationService queries the ARM Providers API to find
// which regions support a given resource type.
type ResourceTypeLocationService struct {
	credentialProvider account.SubscriptionCredentialProvider
	armClientOptions   *arm.ClientOptions
}

// NewResourceTypeLocationService creates a new ResourceTypeLocationService.
func NewResourceTypeLocationService(
	credentialProvider account.SubscriptionCredentialProvider,
	armClientOptions *arm.ClientOptions,
) *ResourceTypeLocationService {
	return &ResourceTypeLocationService{
		credentialProvider: credentialProvider,
		armClientOptions:   armClientOptions,
	}
}

// GetLocations returns the sorted list of Azure locations where the given
// resource type (e.g., "Microsoft.Web/staticSites") is available.
func (s *ResourceTypeLocationService) GetLocations(
	ctx context.Context,
	subscriptionID string,
	fullResourceType string,
) ([]string, error) {
	parts := strings.SplitN(fullResourceType, "/", 2)
	if len(parts) != 2 {
		return nil, nil
	}
	namespace, typeName := parts[0], parts[1]

	cred, err := s.credentialProvider.CredentialForSubscription(ctx, subscriptionID)
	if err != nil {
		return nil, err
	}

	client, err := armresources.NewProviderResourceTypesClient(
		subscriptionID, cred, s.armClientOptions,
	)
	if err != nil {
		return nil, err
	}

	resp, err := client.List(ctx, namespace, nil)
	if err != nil {
		return nil, err
	}

	for _, rt := range resp.Value {
		if rt.ResourceType != nil && strings.EqualFold(*rt.ResourceType, typeName) {
			locations := make([]string, 0, len(rt.Locations))
			for _, loc := range rt.Locations {
				if loc != nil && *loc != "" {
					locations = append(locations, strings.ToLower(*loc))
				}
			}
			slices.Sort(locations)
			return locations, nil
		}
	}

	return nil, nil
}
