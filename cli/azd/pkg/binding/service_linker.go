package binding

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicelinker/armservicelinker"
	azdinternal "github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

// ServiceLinkerService exposes operations for managing Azure Service Linker resources
type ServiceLinkerService interface {
	// Get service linker resource
	Get(
		ctx context.Context,
		subscriptionId string,
		sourceResourceId string,
		linkerName string,
	) (*armservicelinker.LinkerResource, error)
	// Create a new service linker resource
	Create(
		ctx context.Context,
		subscriptionId string,
		sourceResourceId string,
		linkerName string,
		targetResourceId string,
		clientType armservicelinker.ClientType,
	) (*armservicelinker.LinkerResource, error)
	// Delete a service linker resource
	Delete(
		ctx context.Context,
		subscriptionId string,
		sourceResourceId string,
		linkerName string,
	) error
}

// NewServiceLinkerService creates a new ServiceLinkerService
func NewServiceLinkerService(
	credentialProvider account.SubscriptionCredentialProvider,
	httpClient httputil.HttpClient,
) ServiceLinkerService {
	return &serviceLinkerService{
		credentialProvider: credentialProvider,
		httpClient:         httpClient,
		userAgent:          azdinternal.UserAgent(),
	}
}

type serviceLinkerService struct {
	credentialProvider account.SubscriptionCredentialProvider
	httpClient         httputil.HttpClient
	userAgent          string
}

// Get a service linker resource
func (sc *serviceLinkerService) Get(
	ctx context.Context,
	subscriptionId string,
	sourceResourceId string,
	linkerName string,
) (*armservicelinker.LinkerResource, error) {
	linkerClient, err := sc.createServiceLinkerClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	resp, err := linkerClient.Get(ctx, sourceResourceId, linkerName, nil)
	return &resp.LinkerResource, err
}

// Create a service linker resource
func (sc *serviceLinkerService) Create(
	ctx context.Context,
	subscriptionId string,
	sourceResourceId string,
	linkerName string,
	targetResourceId string,
	clientType armservicelinker.ClientType,
) (*armservicelinker.LinkerResource, error) {
	linkerClient, err := sc.createServiceLinkerClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	linkerResource := constructLinkerResource(targetResourceId, clientType)
	poller, err := linkerClient.BeginCreateOrUpdate(ctx, sourceResourceId, linkerName, linkerResource, nil)
	if err != nil {
		return nil, err
	}

	resp, err := poller.PollUntilDone(ctx, nil)
	return &resp.LinkerResource, err
}

// Delete a service linker resource
func (sc *serviceLinkerService) Delete(
	ctx context.Context,
	subscriptionId string,
	sourceResourceId string,
	linkerName string,
) error {
	linkerClient, err := sc.createServiceLinkerClient(ctx, subscriptionId)
	if err != nil {
		return err
	}

	poller, err := linkerClient.BeginDelete(ctx, sourceResourceId, linkerName, nil)
	if err != nil {
		return err
	}

	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

// Create a client to manage service linker resources
func (sc *serviceLinkerService) createServiceLinkerClient(
	ctx context.Context,
	subscriptionId string,
) (*armservicelinker.LinkerClient, error) {
	credential, err := sc.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	options := azsdk.DefaultClientOptionsBuilder(ctx, sc.httpClient, sc.userAgent).BuildArmClientOptions()
	clientFactory, err := armservicelinker.NewClientFactory(credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating Service Linker client: %w", err)
	}

	client := clientFactory.NewLinkerClient()
	return client, nil
}

// Construct a resource payload used for linker resource creation
func constructLinkerResource(
	targetResourceId string,
	clientType armservicelinker.ClientType,
) armservicelinker.LinkerResource {
	// Fixed to use secret as auth type for azd
	secretAuthType := armservicelinker.AuthTypeSecret
	azureResourceType := armservicelinker.TargetServiceTypeAzureResource
	scope := "main"

	return armservicelinker.LinkerResource{
		Properties: &armservicelinker.LinkerProperties{
			AuthInfo: &armservicelinker.SecretAuthInfo{
				AuthType: &secretAuthType,
			},
			TargetService: &armservicelinker.AzureResource{
				Type: &azureResourceType,
				ID:   &targetResourceId,
			},
			ClientType: &clientType,
			Scope:      &scope,
		},
	}
}
