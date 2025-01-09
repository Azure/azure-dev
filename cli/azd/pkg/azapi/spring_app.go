package azapi

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appplatform/armappplatform/v2"
	"github.com/Azure/azure-storage-file-go/azfile"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
)

// SpringService provides artifacts upload/deploy and query to Azure Spring Apps (ASA)
type SpringService interface {
	// Get Spring app properties
	GetSpringAppProperties(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		instanceName string,
		appName string,
	) (*SpringAppProperties, error)
	// Deploy jar artifact to ASA app deployment
	DeploySpringAppArtifact(
		ctx context.Context,
		subscriptionId string,
		resourceGroup string,
		instanceName string,
		appName string,
		relativePath string,
		deploymentName string,
	) (*string, error)
	// Upload jar artifact to ASA app Storage File
	UploadSpringArtifact(
		ctx context.Context,
		subscriptionId string,
		resourceGroup string,
		instanceName string,
		appName string,
		artifactPath string,
	) (*string, error)
	// Get Spring app deployment
	GetSpringAppDeployment(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		instanceName string,
		appName string,
		deploymentName string,
	) (*string, error)
}

type springService struct {
	credentialProvider account.SubscriptionCredentialProvider
	armClientOptions   *arm.ClientOptions
}

// Creates a new instance of the NewSpringService
func NewSpringService(
	credentialProvider account.SubscriptionCredentialProvider,
	armClientOptions *arm.ClientOptions,
) SpringService {
	return &springService{
		credentialProvider: credentialProvider,
		armClientOptions:   armClientOptions,
	}
}

type SpringAppProperties struct {
	Url []string
}

func (ss *springService) GetSpringAppProperties(
	ctx context.Context,
	subscriptionId, resourceGroup, instanceName, appName string,
) (*SpringAppProperties, error) {
	client, err := ss.createSpringAppClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	springApp, err := client.Get(ctx, resourceGroup, instanceName, appName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving spring app properties: %w", err)
	}

	var url []string
	if springApp.Properties != nil &&
		springApp.Properties.URL != nil &&
		*springApp.Properties.Public {
		url = []string{*springApp.Properties.URL}
	} else {
		url = []string{}
	}

	return &SpringAppProperties{
		Url: url,
	}, nil
}

func (ss *springService) UploadSpringArtifact(
	ctx context.Context,
	subscriptionId, resourceGroup, instanceName, appName, artifactPath string,
) (*string, error) {
	file, err := os.Open(artifactPath)

	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("artifact %s does not exist: %w", artifactPath, err)
	}
	if err != nil {
		return nil, fmt.Errorf("reading artifact file %s: %w", artifactPath, err)
	}
	defer file.Close()

	springClient, err := ss.createSpringAppClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}
	storageInfo, err := springClient.GetResourceUploadURL(ctx, resourceGroup, instanceName, appName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get resource upload URL: %w", err)
	}

	url, err := url.Parse(*storageInfo.UploadURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse storage upload url %s : %w", *storageInfo.UploadURL, err)
	}

	// Pass NewAnonymousCredential here, since the URL returned by Azure Spring Apps already contains a SAS token
	fileURL := azfile.NewFileURL(*url, azfile.NewPipeline(azfile.NewAnonymousCredential(), azfile.PipelineOptions{}))
	err = azfile.UploadFileToAzureFile(ctx, file, fileURL,
		azfile.UploadToAzureFileOptions{
			Metadata: azfile.Metadata{
				"createdby": "AZD",
			},
		})

	if err != nil {
		return nil, fmt.Errorf("failed to upload artifact %s : %w", artifactPath, err)
	}

	return storageInfo.RelativePath, nil
}

func (ss *springService) DeploySpringAppArtifact(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	instanceName string,
	appName string,
	relativePath string,
	deploymentName string,
) (*string, error) {
	deploymentClient, err := ss.createSpringAppDeploymentClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	springClient, err := ss.createSpringAppClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	_, err = ss.createOrUpdateDeployment(deploymentClient, ctx, resourceGroup, instanceName, appName,
		deploymentName, relativePath)
	if err != nil {
		return nil, err
	}
	resName, err := ss.activeDeployment(springClient, ctx, resourceGroup, instanceName, appName, deploymentName)
	if err != nil {
		return nil, err
	}

	return resName, nil
}

func (ss *springService) GetSpringAppDeployment(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	instanceName string,
	appName string,
	deploymentName string,
) (*string, error) {
	client, err := ss.createSpringAppDeploymentClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	resp, err := client.Get(ctx, resourceGroupName, instanceName, appName, deploymentName, nil)

	if err != nil {
		return nil, err
	}

	return resp.Name, nil
}

func (ss *springService) createSpringAppClient(
	ctx context.Context,
	subscriptionId string,
) (*armappplatform.AppsClient, error) {
	credential, err := ss.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	client, err := armappplatform.NewAppsClient(subscriptionId, credential, ss.armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating SpringApp client: %w", err)
	}

	return client, nil
}

func (ss *springService) createSpringAppDeploymentClient(
	ctx context.Context,
	subscriptionId string,
) (*armappplatform.DeploymentsClient, error) {
	credential, err := ss.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	client, err := armappplatform.NewDeploymentsClient(subscriptionId, credential, ss.armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating SpringAppDeployment client: %w", err)
	}

	return client, nil
}

func (ss *springService) createOrUpdateDeployment(
	deploymentClient *armappplatform.DeploymentsClient,
	ctx context.Context,
	resourceGroup string,
	instanceName string,
	appName string,
	deploymentName string,
	relativePath string,
) (*string, error) {
	poller, err := deploymentClient.BeginCreateOrUpdate(ctx, resourceGroup, instanceName, appName, deploymentName,
		armappplatform.DeploymentResource{
			Properties: &armappplatform.DeploymentResourceProperties{
				Source: &armappplatform.JarUploadedUserSourceInfo{
					Type:         to.Ptr("Jar"),
					RelativePath: to.Ptr(relativePath),
				},
			},
		}, nil)
	if err != nil {
		return nil, err
	}

	res, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, err
	}

	return res.Name, nil
}

func (ss *springService) activeDeployment(
	springClient *armappplatform.AppsClient,
	ctx context.Context,
	resourceGroup string,
	instanceName string,
	appName string,
	deploymentName string,
) (*string, error) {
	poller, err := springClient.BeginSetActiveDeployments(ctx, resourceGroup, instanceName, appName,
		armappplatform.ActiveDeploymentCollection{
			ActiveDeploymentNames: []*string{
				to.Ptr(deploymentName),
			},
		}, nil)

	if err != nil {
		return nil, err
	}

	res, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, err
	}

	return res.Name, nil
}
