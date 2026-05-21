// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"azure.ai.training/pkg/models"
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

// ListRunArtifacts lists artifacts for a run via the data-plane history endpoint.
//
//	GET .../jobs/{name}/history/experimentids/{experimentId}/runs/{runId}/artifacts
//	    [?path=<prefix>&continuationToken=<token>]
//
// For Jobs, runId matches the job name. Pass an empty string for pathPrefix to
// list all artifacts; pass continuationToken from a previous page to continue.
func (c *Client) ListRunArtifacts(
	ctx context.Context, jobName, experimentID, pathPrefix, continuationToken string,
) (*models.RunArtifactList, error) {
	path := fmt.Sprintf(
		"jobs/%s/history/experimentids/%s/runs/%s/artifacts",
		url.PathEscape(jobName),
		url.PathEscape(experimentID),
		url.PathEscape(jobName),
	)

	var qp []string
	if pathPrefix != "" {
		qp = append(qp, "path", pathPrefix)
	}
	if continuationToken != "" {
		qp = append(qp, "continuationToken", continuationToken)
	}

	resp, err := c.doDataPlane(ctx, http.MethodGet, path, nil, qp...)
	if err != nil {
		return nil, fmt.Errorf("failed to list run artifacts: %w", err)
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

// ListRunArtifactContentInfo lists artifact content info (SAS download URIs) for a run via
// the data-plane history endpoint.
//
//	GET .../jobs/{name}/history/experimentids/{experimentId}/runs/{runId}/artifacts/prefix/contentinfo
//	    [?path=<prefix>&continuationToken=<token>]
//
// For Jobs, runId matches the job name. Pass an empty string for pathPrefix to
// fetch content info for all artifacts; pass continuationToken from a previous
// page to continue.
func (c *Client) ListRunArtifactContentInfo(
	ctx context.Context, jobName, experimentID, pathPrefix, continuationToken string,
) (*models.RunArtifactContentInfoList, error) {
	path := fmt.Sprintf(
		"jobs/%s/history/experimentids/%s/runs/%s/artifacts/prefix/contentinfo",
		url.PathEscape(jobName),
		url.PathEscape(experimentID),
		url.PathEscape(jobName),
	)

	var qp []string
	if pathPrefix != "" {
		qp = append(qp, "path", pathPrefix)
	}
	if continuationToken != "" {
		qp = append(qp, "continuationToken", continuationToken)
	}

	resp, err := c.doDataPlane(ctx, http.MethodGet, path, nil, qp...)
	if err != nil {
		return nil, fmt.Errorf("failed to list run artifact content info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.HandleError(resp)
	}

	var result models.RunArtifactContentInfoList
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode run artifact content info response: %w", err)
	}

	return &result, nil
}
