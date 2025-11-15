// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/pkg/httputil"
)

// TokenForAudience fetches the federated token from the federated token provider.
func (c *FederatedTokenClient) TokenForAudience(ctx context.Context, audience string) (string, error) {
	idTokenUrl := c.idTokenUrl
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

type bearerTokenAuthPolicy struct {
	token string
}

// Do authorizes a request with a bearer token
func (b *bearerTokenAuthPolicy) Do(req *policy.Request) (*http.Response, error) {
	if b.token != "" {
		req.Raw().Header.Set("Authorization", fmt.Sprintf("Bearer %s", b.token))
	}
	return req.Next()
}

// FederatedTokenClient is a client that can be used to fetch federated access tokens from a federated provider.
type FederatedTokenClient struct {
	idTokenUrl string

	pipeline runtime.Pipeline
}

func NewFederatedTokenClient(idTokenUrl string, token string, options azcore.ClientOptions) *FederatedTokenClient {
	pipeline := runtime.NewPipeline("github", "1.0.0", runtime.PipelineOptions{
		PerRetry: []policy.Policy{
			&bearerTokenAuthPolicy{token: token},
		},
	}, &options)

	return &FederatedTokenClient{
		pipeline:   pipeline,
		idTokenUrl: idTokenUrl,
	}
}
