package azcli

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v2"
	azdinternal "github.com/azure/azure-dev/cli/azd/internal"
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
}

type managedClustersService struct {
	userAgent  string
	httpClient httputil.HttpClient
	credential azcore.TokenCredential
}

// Creates a new instance of the ManagedClustersService
func NewManagedClustersService(credential azcore.TokenCredential, httpClient httputil.HttpClient) ManagedClustersService {
	return &managedClustersService{
		credential: credential,
		httpClient: httpClient,
		userAgent:  azdinternal.MakeUserAgentString(""),
	}
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
	options := clientOptionsBuilder(cs.httpClient, cs.userAgent).BuildArmClientOptions()

	client, err := armcontainerservice.NewManagedClustersClient(subscriptionId, cs.credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating managed clusters client, %w", err)
	}

	return client, nil
}
