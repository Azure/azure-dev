// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// GetAppServiceLogStream returns a streaming reader for App Service application logs.
// It connects to the Kudu SCM logstream endpoint and returns an io.ReadCloser that
// streams log data in real-time. The caller is responsible for closing the reader.
// This works for both App Service and Azure Functions targets (Microsoft.Web/sites).
func (cli *AzureClient) GetAppServiceLogStream(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	appName string,
) (io.ReadCloser, error) {
	app, err := cli.appService(ctx, subscriptionId, resourceGroup, appName)
	if err != nil {
		return nil, fmt.Errorf("getting app service properties: %w", err)
	}

	hostName, err := appServiceRepositoryHost(app, appName)
	if err != nil {
		return nil, fmt.Errorf("getting repository host: %w", err)
	}

	credential, err := cli.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("getting credential: %w", err)
	}

	token, err := credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://management.azure.com/.default"},
	})
	if err != nil {
		return nil, fmt.Errorf("getting access token: %w", err)
	}

	logStreamURL := fmt.Sprintf("https://%s/api/logstream", hostName)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, logStreamURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating log stream request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token.Token)

	//nolint:gosec // URL is constructed from trusted Azure ARM data (SCM hostname)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("connecting to log stream: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		return nil, fmt.Errorf(
			"log stream returned HTTP %d: %s", resp.StatusCode, string(body),
		)
	}

	return resp.Body, nil
}
