package azcli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appplatform/armappplatform/v2"
	"github.com/Azure/azure-storage-file-go/azfile"
	azdinternal "github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
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
	// Get ASA instance tier
	GetSpringInstanceTier(
		ctx context.Context,
		subscriptionId string,
		resourceGroup string,
		instanceName string,
	) (*string, error)
	// Create build in BuildService
	CreateBuild(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		instanceName string,
		buildServiceName string,
		agentPoolName string,
		builderName string,
		buildName string,
		jvmVersion string,
		relativePath string,
	) (*string, error)
	// Get build result from BuildService
	GetBuildResult(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		instanceName string,
		buildServiceName string,
		buildName string,
		buildResult string,
	) (*armappplatform.BuildResultProvisioningState, error)
	// Deploy build result
	DeployBuildResult(
		ctx context.Context,
		subscriptionId string,
		resourceGroup string,
		instanceName string,
		appName string,
		buildResult string,
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
		userAgent:          azdinternal.UserAgent(),
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

func (ss *springService) GetSpringInstanceTier(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	instanceName string,
) (*string, error) {
	client, err := ss.createSpringClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}
	resp, err := client.Get(ctx, resourceGroup, instanceName, nil)
	if err != nil {
		return nil, err
	}
	return resp.SKU.Tier, nil
}

func (ss *springService) CreateBuild(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	instanceName string,
	buildServiceName string,
	agentPoolName string,
	builderName string,
	buildName string,
	jvmVersion string,
	relativePath string,
) (*string, error) {
	client, err := ss.createBuildServiceClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

      basePath := "/subscriptions/" + subscriptionId +
		"/resourceGroups/" + resourceGroupName +
		"/providers/Microsoft.AppPlatform/Spring/" + instanceName +
		"/buildServices/" + buildServiceName
	agentPoolId := basePath  +
		"/agentPools/" + agentPoolName
	builderId := basePath  +
		"/builders/" + builderName

	resp, err := client.CreateOrUpdateBuild(ctx, resourceGroupName, instanceName, buildServiceName, buildName,
		armappplatform.Build{
			Properties: &armappplatform.BuildProperties{
				AgentPool: to.Ptr(agentPoolId),
				Builder:   to.Ptr(builderId),
				Env: map[string]*string{
					"BP_JVM_VERSION": to.Ptr(jvmVersion),
				},
				RelativePath: to.Ptr(relativePath),
			},
		}, nil)

	if err != nil {
		return nil, err
	}

	return resp.Properties.TriggeredBuildResult.ID, nil
}

func (ss *springService) GetBuildResult(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	instanceName string,
	buildServiceName string,
	buildName string,
	buildResult string,
) (*armappplatform.BuildResultProvisioningState, error) {
	client, err := ss.createBuildServiceClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	buildResultName := buildResult[strings.LastIndex(buildResult, "/")+1:]
	retries := 0
	const maxRetries = 50
	for {
		resp, err := client.GetBuildResult(ctx, resourceGroupName, instanceName, buildServiceName, buildName, buildResultName, nil)
		if err != nil {
			return nil, err
		}

		if *resp.Properties.ProvisioningState == armappplatform.BuildResultProvisioningStateSucceeded {
			return resp.Properties.ProvisioningState, nil
		} else if *resp.Properties.ProvisioningState == armappplatform.BuildResultProvisioningStateFailed {
			return resp.Properties.ProvisioningState, errors.New("build result failed")
		}
		retries++

		if retries >= maxRetries {
			return nil, errors.New("get build result timeout")
		}

		time.Sleep(20 * time.Second)
	}
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

func (ss *springService) DeployBuildResult(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	instanceName string,
	appName string,
	buildResult string,
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

	_, err = ss.createOrUpdateDeploymentByBuildResult(deploymentClient, ctx, resourceGroup, instanceName, appName,
		deploymentName, buildResult)
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

	options := clientOptionsBuilder(ctx, ss.httpClient, ss.userAgent).BuildArmClientOptions()
	client, err := armappplatform.NewAppsClient(subscriptionId, credential, options)

	if err != nil {
		return nil, fmt.Errorf("creating SpringApp client: %w", err)
	}

	return client, nil
}

func (ss *springService) createSpringClient(
	ctx context.Context,
	subscriptionId string,
) (*armappplatform.ServicesClient, error) {
	credential, err := ss.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	options := clientOptionsBuilder(ctx, ss.httpClient, ss.userAgent).BuildArmClientOptions()
	client, err := armappplatform.NewServicesClient(subscriptionId, credential, options)

	if err != nil {
		return nil, fmt.Errorf("creating SpringApp client: %w", err)
	}

	return client, nil
}

func (ss *springService) createBuildServiceClient(
	ctx context.Context,
	subscriptionId string,
) (*armappplatform.BuildServiceClient, error) {
	credential, err := ss.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	options := clientOptionsBuilder(ctx, ss.httpClient, ss.userAgent).BuildArmClientOptions()
	client, err := armappplatform.NewBuildServiceClient(subscriptionId, credential, options)

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

	options := clientOptionsBuilder(ctx, ss.httpClient, ss.userAgent).BuildArmClientOptions()
	client, err := armappplatform.NewDeploymentsClient(subscriptionId, credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating SpringAppDeployment client: %w", err)
	}

	return client, nil
}

func (ss *springService) createOrUpdateDeploymentByBuildResult(
	deploymentClient *armappplatform.DeploymentsClient,
	ctx context.Context,
	resourceGroup string,
	instanceName string,
	appName string,
	deploymentName string,
	buildResult string,
) (*string, error) {
	poller, err := deploymentClient.BeginCreateOrUpdate(ctx, resourceGroup, instanceName, appName, deploymentName,
		armappplatform.DeploymentResource{
			Properties: &armappplatform.DeploymentResourceProperties{
				Source: &armappplatform.BuildResultUserSourceInfo{
					Type:          to.Ptr("BuildResult"),
					BuildResultID: to.Ptr(buildResult),
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
