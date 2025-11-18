// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package armmsi

import (
	"context"
	"fmt"
	"log"
	"slices"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/azure/azure-dev/pkg/account"
)

// ArmMsiService provides functionality to interact with Azure Managed Service Identity (MSI) resources.
// It uses a subscription credential provider and ARM client options to authenticate and configure requests.
type ArmMsiService struct {
	credentialProvider account.SubscriptionCredentialProvider
	armClientOptions   *arm.ClientOptions
}

func NewArmMsiService(
	credentialProvider account.SubscriptionCredentialProvider,
	armClientOptions *arm.ClientOptions,
) ArmMsiService {
	return ArmMsiService{
		credentialProvider: credentialProvider,
		armClientOptions:   armClientOptions,
	}
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

// ListUserIdentities retrieves a list of user-assigned managed identities within a specified Azure subscription.
//
// Parameters:
//   - ctx: The context.Context for the request
//   - subscriptionId: The Azure subscription ID
//   - resourceGroup: The name of the resource group
//
// Returns:
//   - []armmsi.Identity: A slice of user-assigned managed identities
//   - error: An error if the operation fails, nil otherwise
//
// The function creates a new client using the provided subscription credentials and queries
// the Azure ARM API to list all user-assigned managed identities in the specified resource group.
// It handles pagination automatically and returns the complete list of identities.
func (s *ArmMsiService) ListUserIdentities(
	ctx context.Context, subscriptionId string) ([]armmsi.Identity, error) {
	// Create a new GraphClient for the subscription
	credential, err := s.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	client, err := armmsi.NewUserAssignedIdentitiesClient(subscriptionId, credential, s.armClientOptions)
	if err != nil {
		return nil, err
	}

	pager := client.NewListBySubscriptionPager(nil)

	var identities []*armmsi.Identity
	for pager.More() {
		resp, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing user identities: %w", err)
		}
		identities = append(identities, resp.Value...)
	}

	identitiesResult := make([]armmsi.Identity, len(identities))
	for i, identity := range identities {
		if identity == nil {
			continue
		}
		identitiesResult[i] = *identity
	}

	return identitiesResult, nil
}

// GetUserIdentity retrieves user-assigned managed identity information from Azure.
//
// Parameters:
//   - ctx: The context.Context for the request
//   - resourceId: The fully qualified resource ID of the user-assigned managed identity
//
// Returns:
//   - armmsi.Identity: The managed identity information if successful
//   - error: An error if the operation fails, including:
//   - Error parsing the resource ID
//   - Error getting credentials for the subscription
//   - Error creating the MSI client
//   - Error retrieving the identity from Azure
func (s *ArmMsiService) GetUserIdentity(
	ctx context.Context,
	resourceId string) (armmsi.Identity, error) {
	msiResId, err := arm.ParseResourceID(resourceId)
	if err != nil {
		return armmsi.Identity{}, fmt.Errorf("parsing MSI resource id: %w", err)
	}
	subscriptionId := msiResId.SubscriptionID
	resourceGroup := msiResId.ResourceGroupName
	resourceName := msiResId.Name
	credential, err := s.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return armmsi.Identity{}, err
	}

	client, err := armmsi.NewUserAssignedIdentitiesClient(subscriptionId, credential, s.armClientOptions)
	if err != nil {
		return armmsi.Identity{}, err
	}

	resp, err := client.Get(ctx, resourceGroup, resourceName, nil)
	if err != nil {
		return armmsi.Identity{}, err
	}

	return resp.Identity, nil
}

// CreateFederatedCredential creates or updates a federated identity credential for a managed identity.
//
// Parameters:
//   - ctx: The context.Context for the request
//   - subscriptionId: The Azure subscription ID
//   - resourceGroup: The resource group name containing the managed identity
//   - msiName: The name of the managed identity
//   - name: The name of the federated credential
//   - subject: The subject identifier
//   - issuer: The issuer URL
//   - audiences: A list of audience values that will be valid for the credential
//
// Returns:
//   - FederatedIdentityCredential: The created/updated federated identity credential
//   - error: An error if the operation fails, nil otherwise
func (s *ArmMsiService) CreateFederatedCredential(
	ctx context.Context,
	subscriptionId, resourceGroup, msiName, name, subject, issuer string,
	audiences []string) (armmsi.FederatedIdentityCredential, error) {
	credential, err := s.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return armmsi.FederatedIdentityCredential{}, err
	}

	client, err := armmsi.NewFederatedIdentityCredentialsClient(subscriptionId, credential, s.armClientOptions)
	if err != nil {
		return armmsi.FederatedIdentityCredential{}, err
	}

	audiencesRefs := make([]*string, len(audiences))
	for i, audience := range audiences {
		audiencesRefs[i] = to.Ptr(audience)
	}
	response, err := client.CreateOrUpdate(ctx, resourceGroup, msiName, name,
		armmsi.FederatedIdentityCredential{
			Properties: &armmsi.FederatedIdentityCredentialProperties{
				Subject:   to.Ptr(subject),
				Issuer:    to.Ptr(issuer),
				Audiences: audiencesRefs,
			},
		}, nil)

	if err != nil {
		return armmsi.FederatedIdentityCredential{}, fmt.Errorf("creating federated identity credential: %w", err)
	}
	return response.FederatedIdentityCredential, nil
}

func (s *ArmMsiService) ApplyFederatedCredentials(ctx context.Context,
	subscriptionId, msiResourceId string,
	federatedCredentials []armmsi.FederatedIdentityCredential) ([]armmsi.FederatedIdentityCredential, error) {
	msiData, err := arm.ParseResourceID(msiResourceId)
	if err != nil {
		return nil, fmt.Errorf("parsing MSI resource id: %w", err)
	}
	credential, err := s.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	client, err := armmsi.NewFederatedIdentityCredentialsClient(subscriptionId, credential, s.armClientOptions)
	if err != nil {
		return nil, err
	}

	// Get existing federated identity credentials
	existingCreds := []*armmsi.FederatedIdentityCredential{}
	existingCredsPager := client.NewListPager(msiData.ResourceGroupName, msiData.Name, nil)
	for existingCredsPager.More() {
		resp, err := existingCredsPager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing existing federated identity credentials: %w", err)
		}
		existingCreds = append(existingCreds, resp.Value...)
	}

	result := []armmsi.FederatedIdentityCredential{}
	for _, cred := range federatedCredentials {

		// Check if the credential already exists
		if slices.ContainsFunc(existingCreds, func(existing *armmsi.FederatedIdentityCredential) bool {
			return *existing.Properties.Subject == *cred.Properties.Subject
		}) {
			log.Printf(
				"federated identity credential with subject %s already exists, skipping creation", *cred.Properties.Subject)
			continue
		}

		newCred, err := client.CreateOrUpdate(ctx, msiData.ResourceGroupName, msiData.Name, *cred.Name, cred, nil)
		if err != nil {
			return nil, fmt.Errorf("creating federated identity credential %s: %w", *cred.Name, err)
		}
		result = append(result, newCred.FederatedIdentityCredential)
	}

	return result, nil
}
