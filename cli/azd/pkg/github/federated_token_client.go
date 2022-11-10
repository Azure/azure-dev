// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

// TokenForAudience gets the federated token from GitHub Actions. It follows the same strategy as the
// as the getIDToken function from `@actions/core`.
func (c *FederatedTokenClient) TokenForAudience(ctx context.Context, audience string) (string, error) {
	idTokenUrl, has := os.LookupEnv("ACTIONS_ID_TOKEN_REQUEST_URL")
	if !has {
		return "", errors.New("no ACTIONS_ID_TOKEN_REQUEST_URL set in the environment")
	}

	if audience != "" {
		idTokenUrl = fmt.Sprintf("%s&audience=%s", idTokenUrl, url.QueryEscape(audience))
	}

	req, err := runtime.NewRequest(ctx, http.MethodGet, idTokenUrl)
	if err != nil {
		return "", fmt.Errorf("building request: %w", err)
	}

	res, err := c.pipeline.Do(req)
	if err != nil {
		return "", fmt.Errorf("sending request: %w", err)
	}
	defer res.Body.Close()

	if !runtime.HasStatusCode(res, http.StatusOK) {
		return "", fmt.Errorf("expected 200 response, got: %d", res.StatusCode)
	}

	tokenResponse, err := httputil.ReadRawResponse[tokenResponse](res)
	if err != nil {
		return "", fmt.Errorf("reading body: %w", err)
	}

	if tokenResponse.Value == "" {
		return "", errors.New("no token in response")
	}

	return tokenResponse.Value, nil
}

type tokenResponse struct {
	Value string `json:"value"`
}

type bearerTokenAuthPolicy struct{}

// Do authorizes a request with a bearer token
func (b *bearerTokenAuthPolicy) Do(req *policy.Request) (*http.Response, error) {
	token, has := os.LookupEnv("ACTIONS_ID_TOKEN_REQUEST_TOKEN")
	if !has {
		return nil, errors.New("no ACTIONS_ID_TOKEN_REQUEST_TOKEN set in environment.")
	}

	req.Raw().Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	return req.Next()
}

// FederatedTokenClient is a client that can be used to fetch federated access tokens when running in GitHub actions.
// It provides similar behavior to logic in the `@actions/core` JavaScript package that actions can use.
type FederatedTokenClient struct {
	pipeline runtime.Pipeline
}

func NewFederatedTokenClient(options *policy.ClientOptions) *FederatedTokenClient {
	pipeline := runtime.NewPipeline("github", "1.0.0", runtime.PipelineOptions{
		PerRetry: []policy.Policy{
			&bearerTokenAuthPolicy{},
		},
	}, options)

	return &FederatedTokenClient{
		pipeline: pipeline,
	}
}
