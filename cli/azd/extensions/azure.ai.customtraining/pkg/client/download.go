// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"azure.ai.customtraining/pkg/models"
)

// GetModelVersion retrieves a model asset version.
// GET .../models/{name}/versions/{version}
func (c *Client) GetModelVersion(
	ctx context.Context, modelName, version string,
) (*models.ModelVersion, error) {
	path := fmt.Sprintf("models/%s/versions/%s",
		url.PathEscape(modelName), url.PathEscape(version))

	resp, err := c.doDataPlane(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get model %s version %s: %w", modelName, version, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.HandleError(resp)
	}

	var result models.ModelVersion
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode model version response: %w", err)
	}

	return &result, nil
}

// GetModelCredentials fetches a SAS-credential response for a model version.
// POST .../models/{name}/versions/{version}/credentials
func (c *Client) GetModelCredentials(
	ctx context.Context, modelName, version, blobURI string,
) (*models.CredentialsResponse, error) {
	path := fmt.Sprintf("models/%s/versions/%s/credentials",
		url.PathEscape(modelName), url.PathEscape(version))

	body := &models.ModelCredentialsRequest{
		BlobURI:                  blobURI,
		GenerateBlobLevelReadSas: true,
	}

	resp, err := c.doDataPlane(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch model credentials: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.HandleError(resp)
	}

	var result models.CredentialsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode model credentials response: %w", err)
	}

	return &result, nil
}

// GetDatasetCredentials fetches a SAS-credential response for a data asset version.
// POST .../datasets/{name}/versions/{version}/credentials
// Uses the dataset API version (v1).
func (c *Client) GetDatasetCredentials(
	ctx context.Context, datasetName, version string,
) (*models.CredentialsResponse, error) {
	path := fmt.Sprintf("datasets/%s/versions/%s/credentials",
		url.PathEscape(datasetName), url.PathEscape(version))

	resp, err := c.doDataPlaneWithVersion(ctx, http.MethodPost, path, DatasetAPIVersion, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch dataset credentials: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.HandleError(resp)
	}

	var result models.CredentialsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode dataset credentials response: %w", err)
	}

	return &result, nil
}

// ListRunArtifacts lists artifacts for a run via the AML history service.
// GET https://{region}.api.azureml.ms/history/v1.0/{workspacePath}/experimentids/{expId}/runs/{runId}/artifacts[?continuationToken=...]
func (c *Client) ListRunArtifacts(
	ctx context.Context, trackingEndpoint, experimentID, runID, continuationToken string,
) (*models.RunArtifactList, error) {
	baseURL, workspacePath, err := parseTrackingEndpoint(trackingEndpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse tracking endpoint: %w", err)
	}

	reqURL := fmt.Sprintf("%s/history/v1.0%s/experimentids/%s/runs/%s/artifacts",
		baseURL, workspacePath, url.PathEscape(experimentID), url.PathEscape(runID))
	if continuationToken != "" {
		reqURL += "?continuationToken=" + url.QueryEscape(continuationToken)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if err := c.addAuth(ctx, req, DataPlaneScope); err != nil {
		return nil, err
	}

	if c.debugBody {
		fmt.Printf("[DEBUG] GET %s\n", reqURL)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.HandleError(resp)
	}

	var result models.RunArtifactList
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode run artifacts response: %w", err)
	}

	return &result, nil
}

// GetRunArtifactContentInfo fetches the content info (SAS URI) for a single run artifact.
// GET https://{region}.api.azureml.ms/history/v1.0/{workspacePath}/experimentids/{expId}/runs/{runId}/artifacts/contentinfo?path={path}
func (c *Client) GetRunArtifactContentInfo(
	ctx context.Context, trackingEndpoint, experimentID, runID, artifactPath string,
) (*models.RunArtifactContentInfo, error) {
	baseURL, workspacePath, err := parseTrackingEndpoint(trackingEndpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse tracking endpoint: %w", err)
	}

	reqURL := fmt.Sprintf("%s/history/v1.0%s/experimentids/%s/runs/%s/artifacts/contentinfo?path=%s",
		baseURL, workspacePath,
		url.PathEscape(experimentID), url.PathEscape(runID),
		url.QueryEscape(artifactPath))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if err := c.addAuth(ctx, req, DataPlaneScope); err != nil {
		return nil, err
	}

	if c.debugBody {
		fmt.Printf("[DEBUG] GET %s\n", reqURL)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.HandleError(resp)
	}

	var result models.RunArtifactContentInfo
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode contentinfo response: %w", err)
	}

	return &result, nil
}
