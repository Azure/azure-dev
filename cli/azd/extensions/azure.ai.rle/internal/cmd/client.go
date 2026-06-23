// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	defaultControlPlaneEndpoint = "http://localhost:5000"
	defaultAccountName          = "local"
	defaultProjectName          = "demo"
)

type rleClient struct {
	baseUrl    string
	httpClient *http.Client
}

type environmentManifest struct {
	Name         string `json:"name"`
	AcrImagePath string `json:"acrImagePath"`
}

type environmentResource struct {
	Id           string `json:"id"`
	Name         string `json:"name"`
	ProjectId    string `json:"projectId,omitempty"`
	AcrImagePath string `json:"acrImagePath,omitempty"`
	Version      string `json:"version,omitempty"`
}

type listEnvironmentsResponse struct {
	Value []environmentResource `json:"value"`
}

type environmentVersion struct {
	EnvironmentId string `json:"environmentId,omitempty"`
	ProjectId     string `json:"projectId,omitempty"`
	Version       string `json:"version,omitempty"`
	AcrImagePath  string `json:"acrImagePath,omitempty"`
	CreatedAt     string `json:"createdAtUtc,omitempty"`
}

type sandboxCreateRequest struct {
	Version string `json:"version,omitempty"`
	Cpu     string `json:"cpu,omitempty"`
	Memory  string `json:"memory,omitempty"`
	Disk    string `json:"disk,omitempty"`
}

type sandboxResource struct {
	Id            string `json:"id"`
	ProjectId     string `json:"projectId,omitempty"`
	EnvironmentId string `json:"environmentId,omitempty"`
	Version       string `json:"version,omitempty"`
	DiskImageId   string `json:"diskImageId,omitempty"`
	AdcSandboxId  string `json:"adcSandboxId,omitempty"`
	Url           string `json:"url,omitempty"`
	Status        string `json:"status,omitempty"`
	Error         string `json:"error,omitempty"`
	CreatedAt     string `json:"createdAtUtc,omitempty"`
	UpdatedAt     string `json:"updatedAtUtc,omitempty"`
}

type listSandboxesResponse struct {
	Value []sandboxResource `json:"value"`
}

type rleHTTPError struct {
	statusCode int
	body       string
}

func (e *rleHTTPError) Error() string {
	return fmt.Sprintf("RLE control plane returned HTTP %d: %s", e.statusCode, strings.TrimSpace(e.body))
}

func newRleClient(endpoint string) *rleClient {
	return &rleClient{
		baseUrl: strings.TrimRight(endpoint, "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func resolveControlPlaneEndpoint(endpoint string) string {
	if endpoint != "" {
		return endpoint
	}
	if endpoint = os.Getenv("AZD_RLE_CONTROL_PLANE"); endpoint != "" {
		return endpoint
	}
	if endpoint = os.Getenv("RLE_CONTROL_PLANE"); endpoint != "" {
		return endpoint
	}
	return defaultControlPlaneEndpoint
}

func (c *rleClient) createOrUpdateEnvironment(
	ctx context.Context,
	account string,
	project string,
	environmentId string,
	manifest environmentManifest,
) (*environmentResource, error) {
	_ = account
	_ = environmentId
	path := fmt.Sprintf(
		"/rle/v1.0/projects/%s/environments",
		url.PathEscape(project),
	)

	var result environmentResource
	if err := c.do(ctx, http.MethodPost, path, manifest, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

func (c *rleClient) listEnvironments(
	ctx context.Context,
	account string,
	project string,
) ([]environmentResource, error) {
	_ = account
	path := fmt.Sprintf(
		"/rle/v1.0/projects/%s/environments",
		url.PathEscape(project),
	)

	var result listEnvironmentsResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}

	return result.Value, nil
}

func (c *rleClient) getEnvironment(
	ctx context.Context,
	account string,
	project string,
	environmentId string,
) (*environmentResource, error) {
	_ = account
	path := fmt.Sprintf(
		"/rle/v1.0/projects/%s/environments/%s",
		url.PathEscape(project),
		url.PathEscape(environmentId),
	)

	var result environmentResource
	if err := c.do(ctx, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

func (c *rleClient) listEnvironmentVersions(
	ctx context.Context,
	account string,
	project string,
	environmentId string,
) ([]environmentVersion, error) {
	_ = account
	path := fmt.Sprintf(
		"/rle/v1.0/projects/%s/environments/%s/versions",
		url.PathEscape(project),
		url.PathEscape(environmentId),
	)

	var result []environmentVersion
	if err := c.do(ctx, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}

	return result, nil
}

func (c *rleClient) createSandbox(
	ctx context.Context,
	account string,
	project string,
	environmentId string,
	request sandboxCreateRequest,
) (*sandboxResource, error) {
	_ = account
	path := fmt.Sprintf(
		"/rle/v1.0/projects/%s/environments/%s/sandboxes",
		url.PathEscape(project),
		url.PathEscape(environmentId),
	)

	var result sandboxResource
	if err := c.do(ctx, http.MethodPost, path, request, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

func (c *rleClient) listSandboxes(
	ctx context.Context,
	account string,
	project string,
	environmentId string,
) ([]sandboxResource, error) {
	_ = account
	path := fmt.Sprintf(
		"/rle/v1.0/projects/%s/environments/%s/sandboxes",
		url.PathEscape(project),
		url.PathEscape(environmentId),
	)

	var result listSandboxesResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}

	return result.Value, nil
}

func (c *rleClient) getSandbox(
	ctx context.Context,
	account string,
	project string,
	environmentId string,
	sandboxId string,
) (*sandboxResource, error) {
	_ = account
	path := fmt.Sprintf(
		"/rle/v1.0/projects/%s/environments/%s/sandboxes/%s",
		url.PathEscape(project),
		url.PathEscape(environmentId),
		url.PathEscape(sandboxId),
	)

	var result sandboxResource
	if err := c.do(ctx, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}

	return &result, nil
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

	req, err := http.NewRequestWithContext(ctx, method, c.baseUrl+path, reader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token := os.Getenv("RLE_BEARER_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
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

func newManifest(environmentId string, name string, image string, version string) environmentManifest {
	_ = environmentId
	_ = version
	return environmentManifest{
		Name:         name,
		AcrImagePath: image,
	}
}
