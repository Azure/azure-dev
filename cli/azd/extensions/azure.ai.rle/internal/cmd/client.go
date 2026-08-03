// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const (
	environmentCollectionPath = "/fine_tuning/environments"
	foundryAPIVersion         = "2025-11-15-preview"
	foundryTokenScope         = "https://ai.azure.com/.default" //nolint:gosec // OAuth scope, not a credential
)

type rleClient struct {
	baseUrl    string
	credential azcore.TokenCredential
	httpClient *http.Client
}

var createRleClient = newRleClient

type v1EnvironmentRequest struct {
	Name         string `json:"name,omitempty"`
	AcrImagePath string `json:"acrImagePath"`
	VersionBump  string `json:"versionBump,omitempty"`
}

type environmentResource struct {
	Id                        string `json:"id"`
	ProjectId                 string `json:"projectId,omitempty"`
	Name                      string `json:"name,omitempty"`
	AcrImagePath              string `json:"acrImagePath,omitempty"`
	Version                   string `json:"version,omitempty"`
	CreatedAt                 string `json:"createdAtUtc,omitempty"`
	UpdatedAt                 string `json:"updatedAtUtc,omitempty"`
	VersionLabel              string `json:"versionLabel,omitempty"`
	DiskImageConversionStatus string `json:"diskImageConversionStatus,omitempty"`
	DiskImageConversionError  string `json:"diskImageConversionError,omitempty"`
}

type listEnvironmentsResponse struct {
	Value []environmentResource `json:"value"`
}

type environmentVersionResource struct {
	EnvironmentId string `json:"environmentId"`
	ProjectId     string `json:"projectId,omitempty"`
	Version       string `json:"version,omitempty"`
	AcrImagePath  string `json:"acrImagePath,omitempty"`
	CreatedAt     string `json:"createdAtUtc,omitempty"`
}

type sandboxCreateRequest struct {
	Version string `json:"version,omitempty"`
}

type sandboxResource struct {
	Id            string `json:"id"`
	ProjectId     string `json:"projectId,omitempty"`
	EnvironmentId string `json:"environmentId,omitempty"`
	Version       string `json:"version,omitempty"`
	BaseUrl       string `json:"baseUrl,omitempty"`
	Status        string `json:"status,omitempty"`
	Error         string `json:"error,omitempty"`
	CreatedAt     string `json:"createdAtUtc,omitempty"`
	UpdatedAt     string `json:"updatedAtUtc,omitempty"`
}

type rleHTTPError struct {
	statusCode int
	body       string
}

func (e *rleHTTPError) Error() string {
	return fmt.Sprintf("RLE control plane returned HTTP %d: %s", e.statusCode, strings.TrimSpace(e.body))
}

func serviceError(err error) error {
	return &azdext.ServiceError{
		Message:     err.Error(),
		ServiceName: "rle-control-plane",
		Suggestion: fmt.Sprintf(
			"Ensure the Foundry project endpoint in %s is reachable and enabled for RLE.",
			foundryProjectEndpointEnvVar,
		),
	}
}

func newRleClient(endpoint string) (*rleClient, error) {
	normalizedEndpoint, err := normalizeFoundryProjectEndpoint(endpoint)
	if err != nil {
		return nil, err
	}

	credential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("create Azure credential: %w", err)
	}

	return newRleClientWithCredential(normalizedEndpoint, credential), nil
}

func newRleClientWithCredential(endpoint string, credential azcore.TokenCredential) *rleClient {
	return &rleClient{
		baseUrl:    strings.TrimRight(endpoint, "/"),
		credential: credential,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *rleClient) createV1Environment(
	ctx context.Context,
	request v1EnvironmentRequest,
) (*environmentResource, error) {
	var result environmentResource
	if err := c.do(ctx, http.MethodPost, environmentCollectionPath, request, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

func (c *rleClient) listEnvironments(
	ctx context.Context,
	skip int,
	top int,
) (*listEnvironmentsResponse, error) {
	query := url.Values{}
	query.Set("skip", strconv.Itoa(skip))
	query.Set("top", strconv.Itoa(top))

	var result listEnvironmentsResponse
	if err := c.do(ctx, http.MethodGet, environmentCollectionPath+"?"+query.Encode(), nil, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

func (c *rleClient) getEnvironmentVersion(
	ctx context.Context,
	name string,
	version string,
) (*environmentResource, error) {
	path := fmt.Sprintf(
		"%s/%s/versions/%s",
		environmentCollectionPath,
		url.PathEscape(name),
		url.PathEscape(version),
	)

	var result environmentResource
	if err := c.do(ctx, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

func (c *rleClient) listEnvironmentVersions(
	ctx context.Context,
	name string,
) ([]environmentVersionResource, error) {
	path := fmt.Sprintf("%s/%s/versions", environmentCollectionPath, url.PathEscape(name))

	var result []environmentVersionResource
	if err := c.do(ctx, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}

	return result, nil
}

func (c *rleClient) createSandbox(
	ctx context.Context,
	environmentId string,
	request sandboxCreateRequest,
) (*sandboxResource, error) {
	path := fmt.Sprintf(
		"%s/%s/sandboxes/lease",
		environmentCollectionPath,
		url.PathEscape(environmentId),
	)

	var result sandboxResource
	if err := c.do(ctx, http.MethodPost, path, request, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

func (c *rleClient) getSandbox(
	ctx context.Context,
	environmentId string,
	sandboxId string,
) (*sandboxResource, error) {
	path := sandboxPath(environmentId, sandboxId)

	var result sandboxResource
	if err := c.do(ctx, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

func (c *rleClient) deleteSandbox(
	ctx context.Context,
	environmentId string,
	sandboxId string,
) error {
	return c.do(ctx, http.MethodDelete, sandboxPath(environmentId, sandboxId)+"/release", nil, nil)
}

func sandboxPath(environmentId string, sandboxId string) string {
	return fmt.Sprintf(
		"%s/%s/sandboxes/%s",
		environmentCollectionPath,
		url.PathEscape(environmentId),
		url.PathEscape(sandboxId),
	)
}

func (c *rleClient) do(ctx context.Context, method string, path string, body any, target any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	requestUrl, err := url.Parse(c.baseUrl + path)
	if err != nil {
		return fmt.Errorf("create request URL: %w", err)
	}
	query := requestUrl.Query()
	query.Set("api-version", foundryAPIVersion)
	requestUrl.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, method, requestUrl.String(), reader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if !strings.EqualFold(req.URL.Scheme, "https") {
		return errors.New("RLE control-plane authentication requires an HTTPS Foundry project endpoint")
	}
	token, err := c.credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{foundryTokenScope},
	})
	if err != nil {
		return fmt.Errorf("authenticate to Foundry: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call RLE control plane %s: %w", c.baseUrl, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read RLE response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &rleHTTPError{statusCode: resp.StatusCode, body: string(respBody)}
	}

	if target == nil || len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, target); err != nil {
		return fmt.Errorf("decode RLE response: %w", err)
	}

	return nil
}
