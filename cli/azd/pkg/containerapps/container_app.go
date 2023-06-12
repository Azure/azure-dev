package containerapps

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v2"
	azdinternal "github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
	"github.com/benbjohnson/clock"
	"github.com/sethvargo/go-retry"
)

// ContainerAppService exposes operations for managing Azure Container Apps
type ContainerAppService interface {
	Get(
		ctx context.Context,
		subscriptionId,
		resourceGroup,
		appName string,
	) (*armappcontainers.ContainerApp, error)
	// Gets the ingress configuration for the specified container app
	GetIngressConfiguration(
		ctx context.Context,
		subscriptionId,
		resourceGroup,
		appName string,
	) (*ContainerAppIngressConfiguration, error)
	// Adds and activates a new revision to the specified container app
	AddRevision(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		appName string,
		imageName string,
	) (*armappcontainers.Revision, error)
	// Validates the revision for the specified container app
	ValidateRevision(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		appName string,
		revisionName string,
	) (*armappcontainers.Revision, error)
	// Shifts traffic to the revision for the specified container app
	ShiftTrafficToRevision(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		appName string,
		revisionName string,
	) error
}

// NewContainerAppService creates a new ContainerAppService
func NewContainerAppService(
	credentialProvider account.SubscriptionCredentialProvider,
	httpClient httputil.HttpClient,
	clock clock.Clock,
) ContainerAppService {
	return &containerAppService{
		credentialProvider: credentialProvider,
		httpClient:         httpClient,
		userAgent:          azdinternal.UserAgent(),
		clock:              clock,
	}
}

type containerAppService struct {
	credentialProvider account.SubscriptionCredentialProvider
	httpClient         httputil.HttpClient
	userAgent          string
	clock              clock.Clock
}

type ContainerAppIngressConfiguration struct {
	HostNames []string
}

func (cas *containerAppService) Get(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	appName string,
) (*armappcontainers.ContainerApp, error) {
	appClient, err := cas.createContainerAppsClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	containerAppResponse, err := appClient.Get(ctx, resourceGroupName, appName, nil)
	if err != nil {
		return nil, fmt.Errorf("getting container app: %w", err)
	}

	return &containerAppResponse.ContainerApp, nil
}

// Gets the ingress configuration for the specified container app
func (cas *containerAppService) GetIngressConfiguration(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	appName string,
) (*ContainerAppIngressConfiguration, error) {
	containerApp, err := cas.Get(ctx, subscriptionId, resourceGroup, appName)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving container app properties: %w", err)
	}

	var hostNames []string
	if containerApp.Properties != nil &&
		containerApp.Properties.Configuration != nil &&
		containerApp.Properties.Configuration.Ingress != nil &&
		containerApp.Properties.Configuration.Ingress.Fqdn != nil {
		hostNames = []string{*containerApp.Properties.Configuration.Ingress.Fqdn}
	} else {
		hostNames = []string{}
	}

	return &ContainerAppIngressConfiguration{
		HostNames: hostNames,
	}, nil
}

// Adds and activates a new revision to the specified container app
func (cas *containerAppService) AddRevision(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	appName string,
	imageName string,
) (*armappcontainers.Revision, error) {
	containerApp, err := cas.Get(ctx, subscriptionId, resourceGroupName, appName)
	if err != nil {
		return nil, fmt.Errorf("getting container app: %w", err)
	}

	// Get the latest revision name
	currentRevisionName := *containerApp.Properties.LatestRevisionName
	revisionsClient, err := cas.createRevisionsClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	revisionResponse, err := revisionsClient.GetRevision(ctx, resourceGroupName, appName, currentRevisionName, nil)
	if err != nil {
		return nil, fmt.Errorf("getting revision '%s': %w", currentRevisionName, err)
	}

	// Update the revision with the new image name and suffix
	revision := revisionResponse.Revision
	revision.Properties.Template.RevisionSuffix = convert.RefOf(fmt.Sprintf("azd-%d", cas.clock.Now().Unix()))
	revision.Properties.Template.Containers[0].Image = convert.RefOf(imageName)

	// Update the container app with the new revision
	containerApp.Properties.Template = revision.Properties.Template
	containerApp, err = cas.syncSecrets(ctx, subscriptionId, resourceGroupName, appName, containerApp)
	if err != nil {
		return nil, fmt.Errorf("syncing secrets: %w", err)
	}

	// Update the container app
	err = cas.updateContainerApp(ctx, subscriptionId, resourceGroupName, appName, containerApp)
	if err != nil {
		return nil, fmt.Errorf("updating container app revision: %w", err)
	}

	newRevisionName := fmt.Sprintf("%s--%s", appName, *revision.Properties.Template.RevisionSuffix)
	newRevisionResponse, err := revisionsClient.GetRevision(ctx, resourceGroupName, appName, newRevisionName, nil)
	if err != nil {
		return nil, fmt.Errorf("getting new revision '%s': %w", newRevisionName, err)
	}

	return &newRevisionResponse.Revision, nil
}

func (cas *containerAppService) ValidateRevision(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	appName string,
	revisionName string,
) (*armappcontainers.Revision, error) {
	revisionsClient, err := cas.createRevisionsClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	var result *armappcontainers.Revision

	err = retry.Do(ctx, retry.WithMaxRetries(10, retry.NewConstant(time.Second*5)), func(ctx context.Context) error {
		getRevisionResponse, err := revisionsClient.GetRevision(ctx, resourceGroupName, appName, revisionName, nil)
		if err != nil {
			return fmt.Errorf("getting revision '%s': %w", revisionName, err)
		}

		revision := getRevisionResponse.Revision

		isProvisioned := false
		isHealthy := false

		switch *revision.Properties.ProvisioningState {
		case armappcontainers.RevisionProvisioningStateProvisioning,
			armappcontainers.RevisionProvisioningStateDeprovisioned,
			armappcontainers.RevisionProvisioningStateDeprovisioning:
			return retry.RetryableError(fmt.Errorf("revision '%s' is not ready", revisionName))
		case armappcontainers.RevisionProvisioningStateFailed:
			return fmt.Errorf("revision '%s' failed to provision, %s", revisionName, *revision.Properties.ProvisioningError)
		case armappcontainers.RevisionProvisioningStateProvisioned:
			isProvisioned = true
		}

		switch *revision.Properties.HealthState {
		case armappcontainers.RevisionHealthStateUnhealthy:
			return fmt.Errorf("revision '%s' is unhealthy", revisionName)
		case armappcontainers.RevisionHealthStateNone, armappcontainers.RevisionHealthStateHealthy:
			isHealthy = true
		}

		if isProvisioned && isHealthy {
			result = &revision
			return nil
		}

		return fmt.Errorf("revision '%s' is an unknown state", revisionName)
	})

	if err != nil {
		return nil, fmt.Errorf("validating revision '%s': %w", revisionName, err)
	}

	return result, nil
}

func (cas *containerAppService) ShiftTrafficToRevision(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	appName string,
	revisionName string,
) error {
	containerApp, err := cas.Get(ctx, subscriptionId, resourceGroupName, appName)
	if err != nil {
		return fmt.Errorf("getting container app: %w", err)
	}

	containerApp.Properties.Configuration.Ingress.Traffic = []*armappcontainers.TrafficWeight{
		{
			RevisionName: &revisionName,
			Weight:       convert.RefOf[int32](100),
		},
	}

	err = cas.updateContainerApp(ctx, subscriptionId, resourceGroupName, appName, containerApp)
	if err != nil {
		return fmt.Errorf("updating traffic weights: %w", err)
	}

	return nil
}

func (cas *containerAppService) syncSecrets(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	appName string,
	containerApp *armappcontainers.ContainerApp,
) (*armappcontainers.ContainerApp, error) {
	// If the container app doesn't have any secrets, we don't need to do anything
	if len(containerApp.Properties.Configuration.Secrets) == 0 {
		return containerApp, nil
	}

	appClient, err := cas.createContainerAppsClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	// Copy the secret configuration from the current version
	// Secret values are not returned by the API, so we need to get them separately
	// to ensure the update call succeeds
	secretsResponse, err := appClient.ListSecrets(ctx, resourceGroupName, appName, nil)
	if err != nil {
		return nil, fmt.Errorf("listing secrets: %w", err)
	}

	secrets := []*armappcontainers.Secret{}
	for _, secret := range secretsResponse.SecretsCollection.Value {
		secrets = append(secrets, &armappcontainers.Secret{
			Name:  secret.Name,
			Value: secret.Value,
		})
	}

	containerApp.Properties.Configuration.Secrets = secrets

	return containerApp, nil
}

func (cas *containerAppService) updateContainerApp(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	appName string,
	containerApp *armappcontainers.ContainerApp,
) error {
	appClient, err := cas.createContainerAppsClient(ctx, subscriptionId)
	if err != nil {
		return err
	}

	poller, err := appClient.BeginUpdate(ctx, resourceGroupName, appName, *containerApp, nil)
	if err != nil {
		return fmt.Errorf("begin updating ingress traffic: %w", err)
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("polling for container app update completion: %w", err)
	}

	return nil
}

func (cas *containerAppService) createContainerAppsClient(
	ctx context.Context,
	subscriptionId string,
) (*armappcontainers.ContainerAppsClient, error) {
	credential, err := cas.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	options := azsdk.DefaultClientOptionsBuilder(ctx, cas.httpClient, cas.userAgent).BuildArmClientOptions()
	client, err := armappcontainers.NewContainerAppsClient(subscriptionId, credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating ContainerApps client: %w", err)
	}

	return client, nil
}

func (cas *containerAppService) createRevisionsClient(
	ctx context.Context,
	subscriptionId string,
) (*armappcontainers.ContainerAppsRevisionsClient, error) {
	credential, err := cas.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	options := azsdk.DefaultClientOptionsBuilder(ctx, cas.httpClient, cas.userAgent).BuildArmClientOptions()
	client, err := armappcontainers.NewContainerAppsRevisionsClient(subscriptionId, credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating ContainerApps client: %w", err)
	}

	return client, nil
}
