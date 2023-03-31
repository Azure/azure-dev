package azcli

import (
	"context"
	"fmt"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appplatform/armappplatform"
	"github.com/Azure/azure-storage-file-go/azfile"
	"log"
	"net/url"
	"os"
)

type AzCliSpringAppProperties struct {
	Fqdn []string
}

func (cli *azCli) GetSpringAppProperties(
	ctx context.Context,
	subscriptionId, resourceGroup, instanceName, appName string,
) (*AzCliSpringAppProperties, error) {
	client, err := cli.createSpringAppClient(ctx, subscriptionId)
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

func (cli *azCli) UploadSpringArtifact(
	ctx context.Context,
	subscriptionId, resourceGroup, instanceName, appName, artifactPath string,
) (*string, error) {

	credential, err := cli.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	options := cli.createDefaultClientOptionsBuilder(ctx).BuildArmClientOptions()

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

func (cli *azCli) DeploySpringAppArtifact(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	instanceName string,
	appName string,
	relativePath string,
	deploymentName string,
) (*string, error) {
	deploymentClient, err := cli.createSpringAppDeploymentClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	springClient, err := cli.createSpringAppClient(ctx, subscriptionId)
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

	springClient.BeginSetActiveDeployments(ctx, resourceGroup, instanceName, appName,
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

func (cli *azCli) GetSpringAppDeployment(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	instanceName string,
	appName string,
	deploymentName string,
) (*string, error) {
	client, err := cli.createSpringAppDeploymentClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	resp, err := client.Get(ctx, resourceGroupName, instanceName, appName, deploymentName, nil)

	if err != nil {
		return nil, err
	}

	return resp.Name, nil
}

func (cli *azCli) createSpringAppClient(
	ctx context.Context,
	subscriptionId string,
) (*armappplatform.AppsClient, error) {
	credential, err := cli.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	options := cli.createDefaultClientOptionsBuilder(ctx).BuildArmClientOptions()
	client, err := armappplatform.NewAppsClient(subscriptionId, credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating SpringApp client: %w", err)
	}

	return client, nil
}

func (cli *azCli) createSpringAppDeploymentClient(
	ctx context.Context,
	subscriptionId string,
) (*armappplatform.DeploymentsClient, error) {
	credential, err := cli.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	options := cli.createDefaultClientOptionsBuilder(ctx).BuildArmClientOptions()
	client, err := armappplatform.NewDeploymentsClient(subscriptionId, credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating SpringApp client: %w", err)
	}

	return client, nil
}
