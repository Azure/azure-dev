// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const (
	armBaseURL           = "https://management.azure.com"
	armComputeAPIVersion = "2026-01-15-preview"
)

// DefaultComputeResolver resolves a compute name to a full ARM resource ID
// by calling the ARM control plane GET endpoint.
//
// ARM URL:
//
//	GET https://management.azure.com/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.CognitiveServices/accounts/{account}/computes/{name}?api-version=2026-01-15-preview
//
// When compute GET moves to the data plane, this resolver can be swapped out
// for a DataPlaneComputeResolver without changing any other code.
type DefaultComputeResolver struct {
	subscriptionID string
	resourceGroup  string
	accountName    string
	credential     azcore.TokenCredential
	httpClient     *http.Client
}

// NewDefaultComputeResolver creates a compute resolver that calls the ARM API.
//   - subscriptionID: Azure subscription ID
//   - resourceGroup: resource group containing the AI account
//   - accountName: Azure AI Foundry account name
//   - credential: token credential for ARM scope
func NewDefaultComputeResolver(subscriptionID, resourceGroup, accountName string, credential azcore.TokenCredential) *DefaultComputeResolver {
	return &DefaultComputeResolver{
		subscriptionID: subscriptionID,
		resourceGroup:  resourceGroup,
		accountName:    accountName,
		credential:     credential,
		httpClient:     &http.Client{Timeout: 30 * time.Second},
	}
}

// ResolveCompute calls the ARM API to resolve a compute name to its full ARM resource ID.
// Returns a helpful error message if the user lacks permissions (401/403).
func (r *DefaultComputeResolver) ResolveCompute(ctx context.Context, computeName string) (string, error) {
	reqURL := fmt.Sprintf(
		"%s/subscriptions/%s/resourceGroups/%s/providers/Microsoft.CognitiveServices/accounts/%s/computes/%s?api-version=%s",
		armBaseURL,
		url.PathEscape(r.subscriptionID),
		url.PathEscape(r.resourceGroup),
		url.PathEscape(r.accountName),
		url.PathEscape(computeName),
		armComputeAPIVersion,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create compute request: %w", err)
	}

	// Get ARM-scoped bearer token
	token, err := r.credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://management.azure.com/.default"},
	})
	if err != nil {
		return "", fmt.Errorf("failed to get ARM token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.Token)

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to call ARM compute API: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	// Permission error — guide user to provide full ARM ID instead
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", fmt.Errorf(
			"insufficient permissions to resolve compute '%s'.\n"+
				"  Provide the full ARM resource ID in your YAML instead:\n"+
				"  compute: /subscriptions/%s/resourceGroups/%s/providers/Microsoft.CognitiveServices/accounts/%s/computes/%s",
			computeName, r.subscriptionID, r.resourceGroup, r.accountName, computeName,
		)
	}

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("compute '%s' not found in account '%s'", computeName, r.accountName)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ARM compute API error (%d): %s", resp.StatusCode, string(body))
	}

	// Parse the response to extract the ARM resource ID
	var result struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse compute response: %w", err)
	}

	if result.ID == "" {
		return "", fmt.Errorf("compute '%s' response missing resource ID", computeName)
	}

	fmt.Printf("  ✓ Compute resolved: %s\n", computeName)
	return result.ID, nil
}
