// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"azure.ai.customtraining/pkg/models"
)

// GetRunHistory retrieves run history details for a specific job.
// GET .../history/{runId}
func (c *Client) GetRunHistory(ctx context.Context, runID string) (*models.RunHistory, error) {
	resp, err := c.doDataPlane(ctx, http.MethodGet, fmt.Sprintf("history/%s", runID), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get run history: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, c.HandleError(resp)
	}

	var result models.RunHistory
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode run history response: %w", err)
	}

	return &result, nil
}
