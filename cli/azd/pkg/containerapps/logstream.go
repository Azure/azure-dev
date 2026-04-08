// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package containerapps

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
)

// GetLogStream returns a streaming reader for Container App console logs.
// It discovers the latest revision and replica, obtains an auth token via the
// Container Apps SDK, and connects to the replica container's LogStreamEndpoint.
// The caller is responsible for closing the returned reader.
func (cas *containerAppService) GetLogStream(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	appName string,
) (io.ReadCloser, error) {
	// Get the container app's latest revision name
	containerApp, err := cas.getContainerApp(ctx, subscriptionId, resourceGroup, appName, nil)
	if err != nil {
		return nil, fmt.Errorf("getting container app details: %w", err)
	}

	latestRevision, ok := containerApp.GetString(pathLatestRevisionName)
	if !ok || latestRevision == "" {
		return nil, fmt.Errorf(
			"could not determine latest revision for container app %s", appName,
		)
	}

	// List replicas for the latest revision to find the log stream endpoint
	credential, err := cas.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("getting credential: %w", err)
	}

	replicasClient, err := armappcontainers.NewContainerAppsRevisionReplicasClient(
		subscriptionId, credential, cas.armClientOptions,
	)
	if err != nil {
		return nil, fmt.Errorf("creating replicas client: %w", err)
	}

	replicasResp, err := replicasClient.ListReplicas(
		ctx, resourceGroup, appName, latestRevision, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("listing replicas for revision %s: %w", latestRevision, err)
	}

	logStreamEndpoint := ""
	if len(replicasResp.Value) > 0 {
		replica := replicasResp.Value[0]
		if replica.Properties != nil && len(replica.Properties.Containers) > 0 {
			container := replica.Properties.Containers[0]
			if container.LogStreamEndpoint != nil {
				logStreamEndpoint = *container.LogStreamEndpoint
			}
		}
	}

	if logStreamEndpoint == "" {
		return nil, fmt.Errorf(
			"no running replicas with log stream endpoints found for container app %s "+
				"(revision: %s) - ensure the app is running",
			appName, latestRevision,
		)
	}

	// Get auth token for the Container App to authenticate to the log stream endpoint
	appClient, err := cas.createContainerAppsClient(ctx, subscriptionId, nil)
	if err != nil {
		return nil, fmt.Errorf("creating container apps client: %w", err)
	}

	authResp, err := appClient.GetAuthToken(ctx, resourceGroup, appName, nil)
	if err != nil {
		return nil, fmt.Errorf("getting Container App auth token: %w", err)
	}

	if authResp.Properties == nil || authResp.Properties.Token == nil ||
		*authResp.Properties.Token == "" {
		return nil, fmt.Errorf("Container App auth token is empty")
	}

	// Connect to the log stream endpoint with the auth token
	// Append follow=true and tailLines query parameters for streaming
	streamURL := logStreamEndpoint + "&follow=true&tailLines=300"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating log stream request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+*authResp.Properties.Token)

	//nolint:gosec // URL is from ARM-provided LogStreamEndpoint on the replica container
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("connecting to Container App log stream: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		return nil, fmt.Errorf(
			"Container App log stream returned HTTP %d: %s",
			resp.StatusCode, string(body),
		)
	}

	return resp.Body, nil
}
