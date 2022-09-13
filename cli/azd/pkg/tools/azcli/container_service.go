package azcli

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v2"
)

type ContainerServiceClient interface {
	GetAdminCredentials(ctx context.Context, resourceGroupName string, resourceName string) (*armcontainerservice.CredentialResults, error)
}

type containerServiceClient struct {
	client         *armcontainerservice.ManagedClustersClient
	subscriptionId string
}

func NewContainerServiceClient(subscriptionId string, credential azcore.TokenCredential, options *arm.ClientOptions) (ContainerServiceClient, error) {
	azureClient, err := armcontainerservice.NewManagedClustersClient(subscriptionId, credential, options)
	if err != nil {
		return nil, err
	}

	return &containerServiceClient{
		subscriptionId: subscriptionId,
		client:         azureClient,
	}, nil
}

func (cs *containerServiceClient) GetAdminCredentials(ctx context.Context, resourceGroupName string, resourceName string) (*armcontainerservice.CredentialResults, error) {
	creds, err := cs.client.ListClusterAdminCredentials(ctx, resourceGroupName, resourceName, nil)
	if err != nil {
		return nil, err
	}

	return &creds.CredentialResults, nil
}
