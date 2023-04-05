package azcli

import (
	"context"
	"fmt"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appplatform/armappplatform"
	"github.com/Azure/azure-storage-file-go/azfile"
	azdinternal "github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
	"log"
	"net/url"
	"os"
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
	) (*AzCliSpringAppProperties, error)
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
	httpClient         httputil.HttpClient
	userAgent          string
}

// Creates a new instance of the NewSpringService
func NewSpringService(
	credentialProvider account.SubscriptionCredentialProvider,
	httpClient httputil.HttpClient,
) SpringService {
	return &springService{
		credentialProvider: credentialProvider,
		httpClient:         httpClient,
		userAgent:          azdinternal.MakeUserAgentString(""),
	}
}

type AzCliSpringAppProperties struct {
	Fqdn []string
}

func (ss *springService) GetSpringAppProperties(
	ctx context.Context,
	subscriptionId, resourceGroup, instanceName, appName string,
) (*AzCliSpringAppProperties, error) {
	client, err := ss.createSpringAppClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	springApp, err := client.Get(ctx, resourceGroup, instanceName, appName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving spring app properties: %w", err)
	}

	return &AzCliSpringAppProperties{
		Fqdn: []string{*springApp.Properties.Fqdn},
	}, nil
}

func (ss *springService) UploadSpringArtifact(
	ctx context.Context,
	subscriptionId, resourceGroup, instanceName, appName, artifactPath string,
) (*string, error) {

	credential, err := ss.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	options := clientOptionsBuilder(ss.httpClient, ss.userAgent).BuildArmClientOptions()

	file, err := os.Open(artifactPath)
	if err != nil {
		return nil, fmt.Errorf("artifact do not exists: %s", artifactPath)
	}
	defer file.Close()

	springClient, err := armappplatform.NewAppsClient(subscriptionId, credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating SpringApps client: %w", err)
	}
	storageInfo, err := springClient.GetResourceUploadURL(ctx, resourceGroup, instanceName, appName, nil)
	if err != nil {
		return nil, fmt.Errorf("get resource upload URL failed: %w", err)
	}

	url, _ := url.Parse(*storageInfo.UploadURL)

	fileURL := azfile.NewFileURL(*url, azfile.NewPipeline(azfile.NewAnonymousCredential(), azfile.PipelineOptions{}))
	err = azfile.UploadFileToAzureFile(ctx, file, fileURL,
		azfile.UploadToAzureFileOptions{
			Parallelism: 3,
			Metadata: azfile.Metadata{
				"createdby": "AZD",
			},
		})

	if err != nil {
		return nil, fmt.Errorf("artifact '%s' upload failed: %w", artifactPath, err)
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

	_, err = springClient.BeginSetActiveDeployments(ctx, resourceGroup, instanceName, appName,
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
		log.Fatalf("failed to pull the result: %v", err)
	}

	return res.Name, nil
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

	options := clientOptionsBuilder(ss.httpClient, ss.userAgent).BuildArmClientOptions()
	client, err := armappplatform.NewAppsClient(subscriptionId, credential, options)
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

	options := clientOptionsBuilder(ss.httpClient, ss.userAgent).BuildArmClientOptions()
	client, err := armappplatform.NewDeploymentsClient(subscriptionId, credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating SpringAppDeployment client: %w", err)
	}

	return client, nil
}
