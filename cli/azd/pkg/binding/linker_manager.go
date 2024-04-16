package binding

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicelinker/armservicelinker/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
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
	armClientOptions *arm.ClientOptions,
) LinkerManager {
	return &linkerManager{
		credentialProvider: credentialProvider,
		httpClient:         httpClient,
		armClientOptions:   armClientOptions,
	}
}

type linkerManager struct {
	credentialProvider account.SubscriptionCredentialProvider
	httpClient         httputil.HttpClient
	armClientOptions   *arm.ClientOptions
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

	resp, err := linkerClient.Get(ctx, linkerConfig.SourceId, linkerConfig.Name, nil)
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
		ctx, linkerConfig.SourceId, linkerConfig.Name, linkerResource, nil)
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

	poller, err := linkerClient.BeginDelete(ctx, linkerConfig.SourceId, linkerConfig.Name, nil)
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

	clientFactory, err := armservicelinker.NewClientFactory(credential, sc.armClientOptions)
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
	secretAuthType := armservicelinker.AuthTypeSecret
	azureResourceType := armservicelinker.TargetServiceTypeAzureResource
	secretTypeRawValue := armservicelinker.SecretTypeRawValue
	networkOptOut := armservicelinker.ActionTypeOptOut

	// construct linker resource
	linkerResource := armservicelinker.LinkerResource{
		Properties: &armservicelinker.LinkerProperties{
			TargetService: &armservicelinker.AzureResource{
				Type: &azureResourceType,
				ID:   &linkerConfig.TargetId,
			},
			PublicNetworkSolution: &armservicelinker.PublicNetworkSolution{
				Action: &networkOptOut,
			},
			ConfigurationInfo: &armservicelinker.ConfigurationInfo{
				ConfigurationStore: &armservicelinker.ConfigurationStore{
					AppConfigurationID: &linkerConfig.AppConfigId,
				},
			},
			ClientType: &linkerConfig.ClientType,
		},
	}

	if linkerConfig.TargetType.IsComputeService() {
		// should use easy auth type for compute service when service connector is ready
		// now still use secret auth type
		linkerResource.Properties.AuthInfo = &armservicelinker.SecretAuthInfo{
			AuthType: &secretAuthType,
		}
	} else {
		// use secret auth type for other services
		linkerResource.Properties.AuthInfo = &armservicelinker.SecretAuthInfo{
			AuthType: &secretAuthType,
			Name:     &linkerConfig.DBUserName,
			SecretInfo: &armservicelinker.ValueSecretInfo{
				SecretType: &secretTypeRawValue,
				Value:      &linkerConfig.DBSecret,
			},
		}
	}

	// if keyvault is provided, save secret info in the binding to keyvault
	// and use appconfig to reference the keyvault
	if linkerConfig.KeyVaultId != "" {
		linkerResource.Properties.SecretStore = &armservicelinker.SecretStore{
			KeyVaultID: &linkerConfig.KeyVaultId,
		}
	}

	return linkerResource
}
