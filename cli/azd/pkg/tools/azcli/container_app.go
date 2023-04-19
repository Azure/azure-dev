package azcli

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers"
	azdinternal "github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
	"github.com/benbjohnson/clock"
)

type ContainerAppService interface {
	GetAppProperties(ctx context.Context, subscriptionId, resourceGroup, appName string) (*AzCliContainerAppProperties, error)
	AddRevision(ctx context.Context, subscriptionId string, resourceGroupName string, appName string, imageName string) error
}

func NewContainerAppService(
	credentialProvider account.SubscriptionCredentialProvider,
	httpClient httputil.HttpClient,
	clock clock.Clock,
) ContainerAppService {
	return &containerAppService{
		credentialProvider: credentialProvider,
		httpClient:         httpClient,
		userAgent:          azdinternal.MakeUserAgentString(""),
		clock:              clock,
	}
}

type containerAppService struct {
	credentialProvider account.SubscriptionCredentialProvider
	httpClient         httputil.HttpClient
	userAgent          string
	clock              clock.Clock
}

type AzCliContainerAppProperties struct {
	HostNames []string
}

func (cas *containerAppService) GetAppProperties(
	ctx context.Context,
	subscriptionId, resourceGroup, appName string,
) (*AzCliContainerAppProperties, error) {
	client, err := cas.createContainerAppsClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	containerApp, err := client.Get(ctx, resourceGroup, appName, nil)
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

	return &AzCliContainerAppProperties{
		HostNames: hostNames,
	}, nil
}

func (cas *containerAppService) AddRevision(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	appName string,
	imageName string,
) error {
	appClient, err := cas.createContainerAppsClient(ctx, subscriptionId)
	if err != nil {
		return err
	}

	containerApp, err := appClient.Get(ctx, resourceGroupName, appName, nil)
	if err != nil {
		return fmt.Errorf("getting container app: %w", err)
	}

	updated := containerApp.ContainerApp
	currentRevisionName := *updated.Properties.LatestRevisionName
	revisionsClient, err := cas.createRevisionsClient(ctx, subscriptionId)
	if err != nil {
		return err
	}

	revision, err := revisionsClient.GetRevision(ctx, resourceGroupName, appName, currentRevisionName, nil)
	if err != nil {
		return fmt.Errorf("getting revision '%s': %w", currentRevisionName, err)
	}

	revision.Properties.Template.RevisionSuffix = convert.RefOf(fmt.Sprintf("azd-deploy-%d", cas.clock.Now().Unix()))
	revision.Properties.Template.Containers[0].Image = convert.RefOf(imageName)
	updated.Properties.Template = revision.Properties.Template

	secretsResponse, err := appClient.ListSecrets(ctx, resourceGroupName, appName, nil)
	if err != nil {
		return fmt.Errorf("listing secrets: %w", err)
	}

	secrets := []*armappcontainers.Secret{}
	for _, secret := range secretsResponse.SecretsCollection.Value {
		secrets = append(secrets, &armappcontainers.Secret{
			Name:  secret.Name,
			Value: secret.Value,
		})
	}

	updated.Properties.Configuration.Secrets = secrets

	poller, err := appClient.BeginUpdate(ctx, resourceGroupName, appName, updated, nil)
	if err != nil {
		return fmt.Errorf("begin updating container app: %w", err)
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("polling for update: %w", err)
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

	options := clientOptionsBuilder(cas.httpClient, cas.userAgent).BuildArmClientOptions()
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

	options := clientOptionsBuilder(cas.httpClient, cas.userAgent).BuildArmClientOptions()
	client, err := armappcontainers.NewContainerAppsRevisionsClient(subscriptionId, credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating ContainerApps client: %w", err)
	}

	return client, nil
}
