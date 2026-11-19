// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ProviderConnection contains the endpoint and credentials of a saved API key connection.
type ProviderConnection struct {
	Target string `json:"target"`
	APIKey string `json:"-"`
}

// GetProviderConnection reads credentials using the Foundry project connection API.
func (c *FoundryClient) GetProviderConnection(ctx context.Context, name string) (*ProviderConnection, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("connection name must not be empty")
	}

	reqURL := fmt.Sprintf("%s%s/connections/%s/getConnectionWithCredentials?api-version=%s",
		c.baseURL, c.subPath, url.PathEscape(name), c.apiVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection request: %w", err)
	}
	if err := c.addAuth(ctx, req); err != nil {
		return nil, err
	}

	httpClient := *c.httpClient
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	//nolint:gosec // URL is constructed from the validated project endpoint.
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to read saved connection: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.handleError(resp)
	}

	var result struct {
		Target      string `json:"target"`
		Credentials struct {
			Type          string `json:"type"`
			Key           string `json:"key"`
			Authorization string `json:"Authorization"`
		} `json:"credentials"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode saved connection: %w", err)
	}

	key := result.Credentials.Key
	switch result.Credentials.Type {
	case "ApiKey":
	case "CustomKeys":
		scheme, token, ok := strings.Cut(result.Credentials.Authorization, " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") {
			return nil, fmt.Errorf("CustomKeys connection must contain an Authorization Bearer credential")
		}
		key = token
	default:
		return nil, fmt.Errorf("provider models require an ApiKey or CustomKeys connection")
	}
	if strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("saved connection does not contain an API key")
	}
	return &ProviderConnection{Target: result.Target, APIKey: key}, nil
}
