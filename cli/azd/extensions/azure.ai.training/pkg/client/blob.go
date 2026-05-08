// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// GetBlobContent downloads content from a SAS URI.
// Fetches the full content on each call. No authentication is needed since the
// URL contains a SAS token. The caller is responsible for tracking incremental
// progress (e.g., line counts for log streaming).
func (c *Client) GetBlobContent(ctx context.Context, sasURI string) (string, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sasURI, nil)
	if err != nil {
		return "", 0, fmt.Errorf("failed to create blob request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("blob request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("blob request returned status %d", resp.StatusCode)
	}

	// Cap read to 1MB per call to avoid memory issues with very large blobs
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", 0, fmt.Errorf("failed to read blob content: %w", err)
	}

	return string(body), int64(len(body)), nil
}
