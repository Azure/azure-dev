// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package containerregistry

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

// RemoteBuildManager provides functionality to interact with the Azure Container Registry Remote Build feature.
type RemoteBuildManager struct {
	credentialProvider account.SubscriptionCredentialProvider
	httpClient         httputil.HttpClient
	armClientOptions   *arm.ClientOptions
}

// UploadBuildSource uploads the build source to the specified registry. The returned SourceUploadDefinition may be used
// when scheduling a build to reference the uploaded source.
func (r *RemoteBuildManager) UploadBuildSource(
	ctx context.Context,
	subscriptionID string,
	resourceGroupName string,
	registryName string,
	buildSourcePath string,
) (armcontainerregistry.SourceUploadDefinition, error) {
	cred, err := r.credentialProvider.CredentialForSubscription(ctx, subscriptionID)
	if err != nil {
		return armcontainerregistry.SourceUploadDefinition{}, err
	}

	regClient, err := armcontainerregistry.NewRegistriesClient(subscriptionID, cred, r.armClientOptions)
	if err != nil {
		return armcontainerregistry.SourceUploadDefinition{}, err
	}

	sourceUploadRes, err := regClient.GetBuildSourceUploadURL(ctx, resourceGroupName, registryName, nil)
	if err != nil {
		return armcontainerregistry.SourceUploadDefinition{}, err
	}

	blobClient, err := blockblob.NewClientWithNoCredential(*sourceUploadRes.UploadURL, nil)
	if err != nil {
		return armcontainerregistry.SourceUploadDefinition{}, err
	}

	dockerContext, err := os.Open(buildSourcePath)
	if err != nil {
		return armcontainerregistry.SourceUploadDefinition{}, err
	}
	defer dockerContext.Close()

	_, err = blobClient.UploadFile(context.Background(), dockerContext, nil)
	if err != nil {
		return armcontainerregistry.SourceUploadDefinition{}, err
	}

	return sourceUploadRes.SourceUploadDefinition, nil
}

// RunDockerBuildRequestWithLogs initiates a remote build on the specified registry and streams the logs to the provided
// writer.
func (r *RemoteBuildManager) RunDockerBuildRequestWithLogs(
	ctx context.Context,
	subscriptionID, resourceGroupName, registryName string,
	buildRequest *armcontainerregistry.DockerBuildRequest,
	writer io.Writer,
) error {
	cred, err := r.credentialProvider.CredentialForSubscription(ctx, subscriptionID)
	if err != nil {
		return err
	}

	regClient, err := armcontainerregistry.NewRegistriesClient(subscriptionID, cred, r.armClientOptions)
	if err != nil {
		return err
	}

	runPoller, err := regClient.BeginScheduleRun(ctx, resourceGroupName, registryName, buildRequest, nil)
	if err != nil {
		return err
	}

	resp, err := runPoller.Poll(ctx)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var runResp armcontainerregistry.RegistriesClientScheduleRunResponse
	if err := json.NewDecoder(resp.Body).Decode(&runResp); err != nil {
		return err
	}

	runClient, err := armcontainerregistry.NewRunsClient(subscriptionID, cred, r.armClientOptions)
	if err != nil {
		return err
	}

	logRes, err := runClient.GetLogSasURL(ctx, resourceGroupName, registryName, *runResp.Properties.RunID, nil)
	if err != nil {
		return err
	}

	logBlobClient, err := blockblob.NewClientWithNoCredential(*logRes.LogLink, &blockblob.ClientOptions{
		ClientOptions: r.armClientOptions.ClientOptions,
	})
	if err != nil {
		return err
	}

	err = streamLogs(ctx, logBlobClient, writer)
	if err != nil {
		return err
	}

	return nil
}

// streamLogs streams the logs from the specified blob client to the provided writer, until the log is marked as complete
// or an error occurs.
func streamLogs(ctx context.Context, blobClient *blockblob.Client, writer io.Writer) error {
	var written int64 = 0
	for {
		props, err := blobClient.GetProperties(ctx, nil)
		if err != nil {
			return err
		}

		length := *props.ContentLength
		if (length - written) == 0 {
			if props.Metadata != nil {
				if _, has := props.Metadata["Complete"]; has {
					return nil
				}
			}

			time.Sleep(1 * time.Second)
			continue
		}

		err = func() error {
			res, err := blobClient.DownloadStream(ctx, &blob.DownloadStreamOptions{
				Range: azblob.HTTPRange{
					Offset: written,
					Count:  length - written,
				},
			})
			if err != nil {
				return err
			}
			defer res.Body.Close()
			copied, err := io.Copy(writer, res.Body)
			if err != nil {
				return err
			}
			written += copied
			return nil
		}()
		if err != nil {
			return err
		}
	}
}

func NewRemoteBuildManager(
	credentialProvider account.SubscriptionCredentialProvider,
	httpClient httputil.HttpClient,
	armClientOptions *arm.ClientOptions,
) *RemoteBuildManager {
	return &RemoteBuildManager{
		credentialProvider: credentialProvider,
		httpClient:         httpClient,
		armClientOptions:   armClientOptions,
	}
}
