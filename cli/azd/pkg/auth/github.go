// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

// TokenForAudience gets the federated token from GitHub Actions. It follows the same strategy as the
// as the getIDToken function from `@actions/core`.
func (c *gitHubFederatedTokenClient) TokenForAudience(ctx context.Context, audience string) (string, error) {
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

	if res.StatusCode != 200 {
		return "", fmt.Errorf("expected 200 response, got: %d", res.StatusCode)
	}

	buf := bytes.Buffer{}
	if _, err := io.Copy(&buf, res.Body); err != nil {
		return "", fmt.Errorf("reading body: %w", err)
	}

	var tokenResponse struct {
		Value string `json:"value"`
	}

	if err := json.Unmarshal(buf.Bytes(), &tokenResponse); err != nil {
		return "", fmt.Errorf("unmarshalling response: %w", err)
	}

	if tokenResponse.Value == "" {
		return "", fmt.Errorf("no token in response")
	}

	return tokenResponse.Value, nil
}

type gitHubBearerTokenAuthPolicy struct{}

// Do authorizes a request with a bearer token
func (b *gitHubBearerTokenAuthPolicy) Do(req *policy.Request) (*http.Response, error) {
	token, has := os.LookupEnv("ACTIONS_ID_TOKEN_REQUEST_TOKEN")
	if !has {
		return nil, errors.New("no ACTIONS_ID_TOKEN_REQUEST_TOKEN set in environment.")
	}

	req.Raw().Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	return req.Next()
}

// gitHubFederatedTokenClient is a client that can be used to fetch federated access tokens when running in GitHub actions.
// It provides similar behavior to logic in the `@actions/core` JavaScript package that actions can use.
type gitHubFederatedTokenClient struct {
	pipeline runtime.Pipeline
}

func newGitHubFederatedTokenClient(options *policy.ClientOptions) *gitHubFederatedTokenClient {
	pipeline := runtime.NewPipeline("github", "1.0.0", runtime.PipelineOptions{
		PerRetry: []policy.Policy{
			&gitHubBearerTokenAuthPolicy{},
		},
	}, options)

	return &gitHubFederatedTokenClient{
		pipeline: pipeline,
	}
}
