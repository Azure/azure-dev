package azcli

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v2"
	azdinternal "github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

// ManagedClustersService provides actions on top of Azure Kubernetes Service (AKS) Managed Clusters
type ManagedClustersService interface {
	// Gets the admin credentials for the specified resource
	GetAdminCredentials(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		resourceName string,
	) (*armcontainerservice.CredentialResults, error)
	// Gets the user credentials for the specified resource
	GetUserCredentials(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		resourceName string,
	) (*armcontainerservice.CredentialResults, error)
}

type managedClustersService struct {
	credentialProvider account.SubscriptionCredentialProvider
	httpClient         httputil.HttpClient
	userAgent          string
	cloud              *cloud.Cloud
}

// Creates a new instance of the ManagedClustersService
func NewManagedClustersService(
	credentialProvider account.SubscriptionCredentialProvider,
	httpClient httputil.HttpClient,
	cloud *cloud.Cloud,
) ManagedClustersService {
	return &managedClustersService{
		credentialProvider: credentialProvider,
		httpClient:         httpClient,
		userAgent:          azdinternal.UserAgent(),
		cloud:              cloud,
	}
}

// Gets the user credentials for the specified resource
func (cs *managedClustersService) GetUserCredentials(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	resourceName string,
) (*armcontainerservice.CredentialResults, error) {
	client, err := cs.createManagedClusterClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	credResult, err := client.ListClusterUserCredentials(ctx, resourceGroupName, resourceName, nil)
	if err != nil {
		return nil, err
	}

	return &credResult.CredentialResults, nil
}

// Gets the admin credentials for the specified resource
func (cs *managedClustersService) GetAdminCredentials(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	resourceName string,
) (*armcontainerservice.CredentialResults, error) {
	client, err := cs.createManagedClusterClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	credResult, err := client.ListClusterAdminCredentials(ctx, resourceGroupName, resourceName, nil)
	if err != nil {
		return nil, err
	}

	return &credResult.CredentialResults, nil
}

func (cs *managedClustersService) createManagedClusterClient(
	ctx context.Context,
	subscriptionId string,
) (*armcontainerservice.ManagedClustersClient, error) {
	credential, err := cs.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	options := clientOptionsBuilder(ctx, cs.httpClient, cs.userAgent, cs.cloud).BuildArmClientOptions()

	client, err := armcontainerservice.NewManagedClustersClient(subscriptionId, credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating managed clusters client, %w", err)
	}

	return client, nil
}
