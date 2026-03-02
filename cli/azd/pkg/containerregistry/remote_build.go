// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package containerregistry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/sethvargo/go-retry"
)

// RemoteBuildManager provides functionality to interact with the Azure Container Registry Remote Build feature.
type RemoteBuildManager struct {
	credentialProvider account.SubscriptionCredentialProvider
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

	blobClient, err := blockblob.NewClientWithNoCredential(*sourceUploadRes.UploadURL, &blockblob.ClientOptions{
		ClientOptions: r.armClientOptions.ClientOptions,
	})
	if err != nil {
		return armcontainerregistry.SourceUploadDefinition{}, err
	}

	dockerContext, err := os.Open(buildSourcePath)
	if err != nil {
		return armcontainerregistry.SourceUploadDefinition{}, err
	}
	defer dockerContext.Close()

	_, err = blobClient.UploadFile(ctx, dockerContext, nil)
	if err != nil {
		return armcontainerregistry.SourceUploadDefinition{}, err
	}

	return sourceUploadRes.SourceUploadDefinition, nil
}

// terminalContainerRegistryRunStates is the list of states we consider terminal when waiting for a container registry run
// to complete. Unfortunately, in the current version of the armcontainerregistry package, the poller returned by
// BeginScheduleRun treats all states as terminal and so calling `PollUntilDone` will return even if if the run is still
// progressing.  So we open code this polling loop ourselves.
var terminalContainerRegistryRunStates = []armcontainerregistry.RunStatus{
	armcontainerregistry.RunStatusCanceled,
	armcontainerregistry.RunStatusError,
	armcontainerregistry.RunStatusFailed,
	armcontainerregistry.RunStatusSucceeded,
	armcontainerregistry.RunStatusTimeout,
}

// RunDockerBuildRequestWithLogs initiates a remote build on the specified registry and streams the logs to the provided
// writer.
func (r *RemoteBuildManager) RunDockerBuildRequestWithLogs(
	ctx context.Context,
	subscriptionID, resourceGroupName, registryName string,
	buildRequest *armcontainerregistry.DockerBuildRequest,
	writer io.Writer,
) error {
	tracing.IncrementUsageAttribute(fields.RemoteBuildCount.Int(1))

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

	var buildLog bytes.Buffer

	err = streamLogs(ctx, logBlobClient, io.MultiWriter(&buildLog, writer))
	if err != nil {
		return err
	}

	// Poll until the run is complete - we do this ourselves because the poller returned by BeginScheduleRun treats all
	// states as terminal and so calling `PollUntilDone` will return even if if the run is still progressing.
	//
	// Since the call to streamLogs above will block until the log is complete, we expect the final status will be
	// available shortly, and so we used an exponential backoff with a capped duration and a short initial delay.
	return retry.Do(ctx, retry.WithCappedDuration(30*time.Second, retry.NewExponential(1*time.Second)),
		func(ctx context.Context) error {
			runRes, err := runClient.Get(ctx, resourceGroupName, registryName, *runResp.Properties.RunID, nil)
			if err != nil {
				return err
			}

			if !slices.Contains(terminalContainerRegistryRunStates, *runRes.Properties.Status) {
				return retry.RetryableError(errors.New("remote build still in progress"))
			}

			if *runRes.Properties.Status != armcontainerregistry.RunStatusSucceeded {
				return fmt.Errorf("remote build failed: %v", buildLog.String())
			}

			return nil
		})
}

// streamLogs streams the logs from the specified blob client to the provided writer, until the log is marked as complete
// or an error occurs.
func streamLogs(ctx context.Context, blobClient *blockblob.Client, writer io.Writer) error {
	var written int64 = 0
	return retry.Do(ctx, retry.WithMaxRetries(10, retry.NewConstant(5*time.Second)), func(ctx context.Context) error {
		err := func() error {
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

					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(1 * time.Second):
					}
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
		}()
		var azErr *azcore.ResponseError
		if errors.As(err, &azErr) {
			if azErr.StatusCode == http.StatusNotFound {
				// Mark log not found as a retryable error, we assume that the blob client was formed around a result from
				// the queue job request and the fact that the log is not found means that the log is not yet available, not
				// that it will never be available.
				return retry.RetryableError(err)
			}
		}
		return err
	})
}

func NewRemoteBuildManager(
	credentialProvider account.SubscriptionCredentialProvider,
	armClientOptions *arm.ClientOptions,
) *RemoteBuildManager {
	return &RemoteBuildManager{
		credentialProvider: credentialProvider,
		armClientOptions:   armClientOptions,
	}
}
