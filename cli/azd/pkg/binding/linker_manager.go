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

// LinkerManager exposes operations for managing Azure Service Linker resources
type LinkerManager interface {
	// Get service linker resource
	Get(
		ctx context.Context,
		subscriptionId string,
		linkerConfig *LinkerConfig,
	) (*armservicelinker.LinkerResource, error)
	// Create a new service linker resource
	Create(
		ctx context.Context,
		subscriptionId string,
		linkerConfig *LinkerConfig,
	) (*armservicelinker.LinkerResource, error)
	// Delete a service linker resource
	Delete(
		ctx context.Context,
		subscriptionId string,
		linkerConfig *LinkerConfig,
	) error
}

// NewLinkerManager creates a new LinkerManager
func NewLinkerManager(
	credentialProvider account.SubscriptionCredentialProvider,
	httpClient httputil.HttpClient,
) LinkerManager {
	return &linkerManager{
		credentialProvider: credentialProvider,
		httpClient:         httpClient,
		userAgent:          azdinternal.UserAgent(),
	}
}

type linkerManager struct {
	credentialProvider account.SubscriptionCredentialProvider
	httpClient         httputil.HttpClient
	userAgent          string
}

// Get a service linker resource
func (sc *linkerManager) Get(
	ctx context.Context,
	subscriptionId string,
	linkerConfig *LinkerConfig,
) (*armservicelinker.LinkerResource, error) {
	linkerClient, err := sc.createServiceLinkerClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	resp, err := linkerClient.Get(ctx, linkerConfig.SourceResourceId, linkerConfig.Name, nil)
	return &resp.LinkerResource, err
}

// Create a service linker resource
func (sc *linkerManager) Create(
	ctx context.Context,
	subscriptionId string,
	linkerConfig *LinkerConfig,
) (*armservicelinker.LinkerResource, error) {
	linkerClient, err := sc.createServiceLinkerClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	linkerResource := constructLinkerResource(linkerConfig)
	poller, err := linkerClient.BeginCreateOrUpdate(
		ctx, linkerConfig.SourceResourceId, linkerConfig.Name, linkerResource, nil)
	if err != nil {
		return nil, err
	}

	resp, err := poller.PollUntilDone(ctx, nil)
	return &resp.LinkerResource, err
}

// Delete a service linker resource
func (sc *linkerManager) Delete(
	ctx context.Context,
	subscriptionId string,
	linkerConfig *LinkerConfig,
) error {
	linkerClient, err := sc.createServiceLinkerClient(ctx, subscriptionId)
	if err != nil {
		return err
	}

	poller, err := linkerClient.BeginDelete(ctx, linkerConfig.SourceResourceId, linkerConfig.Name, nil)
	if err != nil {
		return err
	}

	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

// Create a client to manage service linker resources
func (sc *linkerManager) createServiceLinkerClient(
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
		return nil, fmt.Errorf("creating service linker client: %w", err)
	}

	client := clientFactory.NewLinkerClient()
	return client, nil
}

// Construct a resource payload used for linker resource creation
func constructLinkerResource(
	linkerConfig *LinkerConfig,
) armservicelinker.LinkerResource {
	// Fixed to use secret as auth type for azd
	secretAuthType := armservicelinker.AuthTypeSecret
	azureResourceType := armservicelinker.TargetServiceTypeAzureResource

	return armservicelinker.LinkerResource{
		Properties: &armservicelinker.LinkerProperties{
			AuthInfo: &armservicelinker.SecretAuthInfo{
				AuthType: &secretAuthType,
			},
			TargetService: &armservicelinker.AzureResource{
				Type: &azureResourceType,
				ID:   &linkerConfig.TargetResourceId,
			},
			ClientType: &linkerConfig.ClientType,
			Scope:      &linkerConfig.Scope,
		},
	}
}
