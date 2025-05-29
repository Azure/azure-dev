// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package armmsi

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
)

// ArmMsiService provides functionality to interact with Azure Managed Service Identity (MSI) resources.
// It uses a subscription credential provider and ARM client options to authenticate and configure requests.
type ArmMsiService struct {
	credentialProvider account.SubscriptionCredentialProvider
	armClientOptions   *arm.ClientOptions
}

// NewArmMsiService creates a new instance of ArmMsiService.
// It takes a SubscriptionCredentialProvider for managing credentials and
// an optional arm.ClientOptions for configuring the ARM client.
//
// Parameters:
//   - credentialProvider: Provides credentials for the subscription.
//   - armClientOptions: Optional configuration options for the ARM client.
//
// Returns:
//
//	An initialized ArmMsiService instance.
//	- error: An error object if the operation fails, otherwise nil.
func (s *ArmMsiService) CreateUserIdentity(
	ctx context.Context,
	subscriptionId, resourceGroup, location, name string) (armmsi.Identity, error) {

	// Create a new GraphClient for the subscription
	credential, err := s.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return armmsi.Identity{}, err
	}

	client, err := armmsi.NewUserAssignedIdentitiesClient(subscriptionId, credential, s.armClientOptions)
	if err != nil {
		return armmsi.Identity{}, err
	}

	msi, err := client.CreateOrUpdate(
		ctx, resourceGroup, name, armmsi.Identity{
			Location: to.Ptr(location),
		}, nil)
	if err != nil {
		return armmsi.Identity{}, err
	}

	return msi.Identity, nil
}
