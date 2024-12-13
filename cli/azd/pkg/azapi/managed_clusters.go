package azapi

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
)

// ManagedClustersService provides actions on top of Azure Kubernetes Service (AKS) Managed Clusters
type ManagedClustersService interface {
	// Gets the managed cluster resource by name
	Get(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		resourceName string,
	) (*armcontainerservice.ManagedCluster, error)
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
	armClientOptions   *arm.ClientOptions
}

// Creates a new instance of the ManagedClustersService
func NewManagedClustersService(
	credentialProvider account.SubscriptionCredentialProvider,
	armClientOptions *arm.ClientOptions,
) ManagedClustersService {
	return &managedClustersService{
		credentialProvider: credentialProvider,
		armClientOptions:   armClientOptions,
	}
}

// Gets the managed cluster resource by name
func (cs *managedClustersService) Get(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	resourceName string,
) (*armcontainerservice.ManagedCluster, error) {
	client, err := cs.createManagedClusterClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	managedCluster, err := client.Get(ctx, resourceGroupName, resourceName, nil)
	if err != nil {
		return nil, err
	}

	return &managedCluster.ManagedCluster, nil
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

func (cs *managedClustersService) createManagedClusterClient(
	ctx context.Context,
	subscriptionId string,
) (*armcontainerservice.ManagedClustersClient, error) {
	credential, err := cs.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	client, err := armcontainerservice.NewManagedClustersClient(subscriptionId, credential, cs.armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating managed clusters client, %w", err)
	}

	return client, nil
}
