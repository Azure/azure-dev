// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"azure.ai.customtraining/pkg/models"
)

// ListArtifacts lists all artifacts for a job.
// GET .../jobs/{id}/artifacts
func (c *Client) ListArtifacts(ctx context.Context, jobID string) (*models.ArtifactList, error) {
	resp, err := c.doDataPlane(
		ctx, http.MethodGet, fmt.Sprintf("jobs/%s/artifacts", jobID), nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list artifacts: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, c.HandleError(resp)
	}

	var result models.ArtifactList
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode artifacts response: %w", err)
	}

	return &result, nil
}

// ListAllArtifacts pages through all artifacts for a job.
func (c *Client) ListAllArtifacts(ctx context.Context, jobID string) ([]models.Artifact, error) {
	result, err := c.ListArtifacts(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	return result.Value, nil
}

// ListArtifactsInPath lists artifacts under a specific path prefix.
// GET .../jobs/{id}/artifacts/path?path={prefix}
func (c *Client) ListArtifactsInPath(
	ctx context.Context, jobID string, pathPrefix string,
) (*models.ArtifactList, error) {
	resp, err := c.doDataPlane(
		ctx, http.MethodGet, fmt.Sprintf("jobs/%s/artifacts/path", jobID), nil,
		"path", pathPrefix,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list artifacts in path: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, c.HandleError(resp)
	}

	var result models.ArtifactList
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode artifacts response: %w", err)
	}

	return &result, nil
}

// GetArtifactContent retrieves raw artifact content.
// GET .../jobs/{id}/artifacts/getcontent/{path}
//
// Returns the response body as an io.ReadCloser along with custom headers.
// The caller is responsible for closing the reader.
func (c *Client) GetArtifactContent(
	ctx context.Context, jobID string, artifactPath string,
	opts *ArtifactContentOptions,
) (*ArtifactContentResponse, error) {
	var queryParams []string
	if opts != nil {
		if opts.Offset != nil {
			queryParams = append(queryParams, "offset", fmt.Sprintf("%d", *opts.Offset))
		}
		if opts.Length != nil {
			queryParams = append(queryParams, "length", fmt.Sprintf("%d", *opts.Length))
		}
		if opts.TailBytes != nil {
			queryParams = append(queryParams, "tailBytes", fmt.Sprintf("%d", *opts.TailBytes))
		}
	}

	escapedPath := url.PathEscape(artifactPath)
	resp, err := c.doDataPlane(
		ctx, http.MethodGet,
		fmt.Sprintf("jobs/%s/artifacts/getcontent/%s", jobID, escapedPath),
		nil, queryParams...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get artifact content: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, c.HandleError(resp)
	}

	return &ArtifactContentResponse{
		Body:          resp.Body,
		ContentLength: resp.Header.Get("X-VW-Content-Length"),
		JobStatus:     resp.Header.Get("X-VW-Job-Status"),
	}, nil
}

// ArtifactContentOptions contains optional parameters for fetching artifact content.
type ArtifactContentOptions struct {
	Offset    *int64
	Length    *int64
	TailBytes *int64
}

// ArtifactContentResponse wraps the raw artifact content with metadata headers.
type ArtifactContentResponse struct {
	Body          io.ReadCloser
	ContentLength string // Total artifact size from X-VW-Content-Length
	JobStatus     string // Current job status from X-VW-Job-Status
}

// GetArtifactContentInfo retrieves content info (including SAS URI) for a single artifact.
// GET .../jobs/{id}/artifacts/contentinfo?path={path}
func (c *Client) GetArtifactContentInfo(
	ctx context.Context, jobID string, artifactPath string,
) (*models.ArtifactContentInfo, error) {
	resp, err := c.doDataPlane(
		ctx, http.MethodGet, fmt.Sprintf("jobs/%s/artifacts/contentinfo", jobID), nil,
		"path", artifactPath,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get artifact content info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, c.HandleError(resp)
	}

	var result models.ArtifactContentInfo
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode artifact content info: %w", err)
	}

	return &result, nil
}

// GetArtifactSASForPath retrieves SAS URIs for all artifacts under a path prefix.
// GET .../jobs/{id}/artifacts/prefix/contentinfo?path={prefix}
func (c *Client) GetArtifactSASForPath(
	ctx context.Context, jobID string, pathPrefix string,
) (*models.ArtifactContentInfoList, error) {
	var queryParams []string
	if pathPrefix != "" {
		queryParams = append(queryParams, "path", pathPrefix)
	}

	resp, err := c.doDataPlane(
		ctx, http.MethodGet, fmt.Sprintf("jobs/%s/artifacts/prefix/contentinfo", jobID), nil,
		queryParams...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get artifact SAS URIs: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, c.HandleError(resp)
	}

	var result models.ArtifactContentInfoList
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode artifact SAS response: %w", err)
	}

	return &result, nil
}

// GetAllArtifactSASForPath pages through all SAS URIs for artifacts under a path prefix.
func (c *Client) GetAllArtifactSASForPath(
	ctx context.Context, jobID string, pathPrefix string,
) ([]models.ArtifactContentInfo, error) {
	result, err := c.GetArtifactSASForPath(ctx, jobID, pathPrefix)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	return result.Value, nil
}
