package appservice

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice"
	azdinternal "github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
	"github.com/benbjohnson/clock"
)

type AppServiceService interface {
	// Adds and activates a new revision to the specified container app
	AddRevision(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		appName string,
		imageName string,
	) error
}

type appServiceService struct {
	credentialProvider account.SubscriptionCredentialProvider
	httpClient         httputil.HttpClient
	userAgent          string
	clock              clock.Clock
}

func NewAppServiceService(
	credentialProvider account.SubscriptionCredentialProvider,
	httpClient httputil.HttpClient,
	clock clock.Clock,
) AppServiceService {
	return &appServiceService{
		credentialProvider: credentialProvider,
		httpClient:         httpClient,
		userAgent:          azdinternal.UserAgent(),
		clock:              clock,
	}
}

func (cas *appServiceService) createWebAppClient(
	ctx context.Context,
	subscriptionId string,
) (*armappservice.WebAppsClient, error) {
	credential, err := cas.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	options := azsdk.DefaultClientOptionsBuilder(ctx, cas.httpClient, cas.userAgent).BuildArmClientOptions()
	client, err := armappservice.NewWebAppsClient(subscriptionId, credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating ContainerApps client: %w", err)
	}

	return client, nil
}

func (as *appServiceService) getWebApp(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	appName string,
) (*armappservice.Site, error) {
	appClient, err := as.createWebAppClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	appServiceResponse, err := appClient.Get(ctx, resourceGroupName, appName, nil)
	if err != nil {
		return nil, fmt.Errorf("getting container app: %w", err)
	}

	return &appServiceResponse.Site, nil
}

// Adds and activates a new revision to the specified container app
func (as *appServiceService) AddRevision(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	appName string,
	imageName string,
) error {
	webApp, err := as.getWebApp(ctx, subscriptionId, resourceGroupName, appName)
	if err != nil {
		return fmt.Errorf("getting container app: %w", err)
	}

	webApp.Properties.SiteConfig.LinuxFxVersion = &imageName

	appClient, err := as.createWebAppClient(ctx, subscriptionId)

	if err != nil {
		return err
	}

	linuxFx := fmt.Sprintf("docker|%s", imageName)
	appClient.Update(ctx, resourceGroupName, appName, armappservice.SitePatchResource{
		Properties: &armappservice.SitePatchResourceProperties{
			SiteConfig: &armappservice.SiteConfig{
				LinuxFxVersion: &linuxFx,
			},
		},
	}, nil)

	return nil
}
