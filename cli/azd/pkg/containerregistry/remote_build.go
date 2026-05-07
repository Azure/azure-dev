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
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
	"github.com/google/uuid"
	"github.com/sethvargo/go-retry"
)

// uniqueCorrelationPolicy is an azcore PerCall policy that overrides the x-ms-correlation-request-id header with a
// freshly generated UUID on every outgoing HTTP request.
//
// The ACR data plane derives the relative blob path it returns from GetBuildSourceUploadURL
// (tasks-source/<yyyymmdd>/<correlationId>.tar.gz) from the caller's x-ms-correlation-request-id. azd's default
// correlation policy (azsdk.NewMsCorrelationPolicy) sets that header from the root OpenTelemetry trace id, which
// is intentionally shared across every HTTP call made within a single azd command invocation — the Azure ARM spec
// defines x-ms-correlation-request-id as a session-level header meant to correlate RELATED requests, so that
// trace-derived behavior is correct for ARM in general. When multiple services upload build sources in parallel
// under the same trace, however, each call receives the same ACR-computed blob path and overlapping writes
// clobber each other, causing cross-contamination where one service's image ends up with another service's
// source code. Forcing a unique correlation id per request here (for the ACR upload client only) guarantees ACR
// hands back a distinct blob path for each upload without weakening the session-level correlation semantics for
// every other ARM call in the command.
//
// This is separate from — and complementary to — the per-request uniqueness that
// azsdk.NewMsClientRequestIdPolicy and azsdk.NewMsGraphCorrelationPolicy apply to the x-ms-client-request-id and
// Graph client-request-id headers respectively. Those policies address a different header that the spec requires
// to be unique per HTTP request globally; this policy addresses the ACR-specific need to also vary the
// correlation header for parallel source uploads.
type uniqueCorrelationPolicy struct{}

func (uniqueCorrelationPolicy) Do(req *policy.Request) (*http.Response, error) {
	req.Raw().Header.Set(azsdk.MsCorrelationIdHeader, uuid.NewString())
	return req.Next()
}

// remoteBuildArmClientOptions returns a copy of the base arm.ClientOptions with a PerCall policy that guarantees a
// unique x-ms-correlation-request-id per request. Used for ACR registry client calls that would otherwise collide on
// ACR's correlation-id-derived blob path for parallel source uploads.
func remoteBuildArmClientOptions(base *arm.ClientOptions) *arm.ClientOptions {
	opts := arm.ClientOptions{}
	if base != nil {
		opts = *base
		opts.ClientOptions.PerCallPolicies = append(
			append([]policy.Policy{}, base.ClientOptions.PerCallPolicies...),
			uniqueCorrelationPolicy{},
		)
	} else {
		opts.ClientOptions.PerCallPolicies = []policy.Policy{uniqueCorrelationPolicy{}}
	}
	return &opts
}

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

	uploadClientOptions := remoteBuildArmClientOptions(r.armClientOptions)
	regClient, err := armcontainerregistry.NewRegistriesClient(subscriptionID, cred, uploadClientOptions)
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
// or an error occurs. The inner polling loop is bounded to prevent runaway iteration.
func streamLogs(ctx context.Context, blobClient *blockblob.Client, writer io.Writer) error {
	const maxPollIterations = 1200 // ~20 minutes at 1s intervals

	var written int64 = 0
	return retry.Do(ctx, retry.WithMaxRetries(10, retry.NewConstant(5*time.Second)), func(ctx context.Context) error {
		err := func() error {
			for iteration := 0; ; iteration++ {
				if iteration >= maxPollIterations {
					return fmt.Errorf(
						"streamLogs exceeded maximum poll iterations (%d); remote build may still be running",
						maxPollIterations,
					)
				}

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
		if azErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
			if azErr.StatusCode == http.StatusNotFound {
				// Mark log not found as a retryable error, we assume that the blob client was formed around a result from
				// the queue job request and the fact that the log is not found means that the log is not yet available, not
				// that it will never be available.
				return retry.RetryableError(err)
			}
			// Wrap non-404 HTTP errors with additional context about the operation and URL.
			return fmt.Errorf("streaming remote build logs (HTTP %d): %w", azErr.StatusCode, err)
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
