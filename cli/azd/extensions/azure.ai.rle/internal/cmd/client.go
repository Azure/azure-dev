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
	"os"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const (
	defaultControlPlaneEndpoint = "http://localhost:5000"
)

type rleClient struct {
	baseUrl    string
	httpClient *http.Client
}

type v1EnvironmentRequest struct {
	Name         string `json:"name,omitempty"`
	AcrImagePath string `json:"acrImagePath"`
}

type environmentResource struct {
	Id           string `json:"id"`
	ProjectId    string `json:"projectId,omitempty"`
	Name         string `json:"name,omitempty"`
	AcrImagePath string `json:"acrImagePath,omitempty"`
	Version      string `json:"version,omitempty"`
	CreatedAtUtc string `json:"createdAtUtc,omitempty"`
	UpdatedAtUtc string `json:"updatedAtUtc,omitempty"`
	VersionLabel string `json:"versionLabel,omitempty"`
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
			"Ensure the RLE control plane is running and reachable, e.g. %s.",
			defaultControlPlaneEndpoint,
		),
	}
}

// isNotFoundError reports whether err is an RLE control plane error with HTTP 404 status.
func isNotFoundError(err error) bool {
	if httpErr, ok := errors.AsType[*rleHTTPError](err); ok {
		return httpErr.statusCode == http.StatusNotFound
	}
	return false
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
	if endpoint = os.Getenv("RLE_ENDPOINT"); endpoint != "" {
		return endpoint
	}
	return defaultControlPlaneEndpoint
}

func (c *rleClient) createV1Environment(
	ctx context.Context,
	project string,
	request v1EnvironmentRequest,
) (*environmentResource, error) {
	path := fmt.Sprintf(
		"/rle/v1.0/projects/%s/environments",
		url.PathEscape(project),
	)

	var result environmentResource
	if err := c.do(ctx, http.MethodPost, path, request, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

func (c *rleClient) updateV1Environment(
	ctx context.Context,
	project string,
	environmentId string,
	request v1EnvironmentRequest,
) (*environmentResource, error) {
	path := fmt.Sprintf(
		"/rle/v1.0/projects/%s/environments/%s",
		url.PathEscape(project),
		url.PathEscape(environmentId),
	)

	var result environmentResource
	if err := c.do(ctx, http.MethodPut, path, request, &result); err != nil {
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
