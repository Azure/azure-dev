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

const (
	// armComputeAPIVersion is the ARM API version for compute operations.
	// When compute GET moves to the data plane, this file can be removed
	// in favor of a data plane compute method.
	armComputeAPIVersion = "2026-01-15-preview"
)

// GetCompute retrieves a compute resource by name from the ARM control plane.
//
// ARM URL:
//
//	GET https://management.azure.com/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.CognitiveServices/accounts/{account}/computes/{name}?api-version=2026-01-15-preview
//
// Requires SetARMContext to be called first.
func (c *Client) GetCompute(ctx context.Context, computeName string) (*models.ComputeResource, error) {
	if c.subscriptionID == "" || c.resourceGroup == "" || c.accountName == "" {
		return nil, fmt.Errorf("ARM context not configured; call SetARMContext first")
	}

	path := fmt.Sprintf(
		"subscriptions/%s/resourceGroups/%s/providers/Microsoft.CognitiveServices/accounts/%s/computes/%s",
		url.PathEscape(c.subscriptionID),
		url.PathEscape(c.resourceGroup),
		url.PathEscape(c.accountName),
		url.PathEscape(computeName),
	)

	resp, err := c.doARM(ctx, http.MethodGet, path, nil, armComputeAPIVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to call ARM compute API: %w", err)
	}
	defer resp.Body.Close()

	// Permission error — guide user to provide full ARM ID instead
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf(
			"insufficient permissions to resolve compute '%s'.\n"+
				"  Provide the full ARM resource ID in your YAML instead:\n"+
				"  compute: /subscriptions/%s/resourceGroups/%s/providers/Microsoft.CognitiveServices/accounts/%s/computes/%s",
			computeName, c.subscriptionID, c.resourceGroup, c.accountName, computeName,
		)
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("compute '%s' not found in account '%s'", computeName, c.accountName)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, c.HandleError(resp)
	}

	var result models.ComputeResource
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to parse compute response: %w", err)
	}

	if result.ID == "" {
		return nil, fmt.Errorf("compute '%s' response missing resource ID", computeName)
	}

	return &result, nil
}
