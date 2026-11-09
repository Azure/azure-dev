// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package fireworks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var resourceIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-][a-zA-Z0-9._-]*$`)
var messageURLPattern = regexp.MustCompile(`https?://[^\s"'<>]+`)

// Client calls the Fireworks management API, never a storage destination.
type Client struct {
	endpoint   string
	apiKey     string
	httpClient *http.Client
}

// Model is the discovery view returned by the Fireworks account models API.
type Model struct {
	Name  string `json:"name" table:"Name"`
	Kind  string `json:"kind,omitempty" table:"Kind"`
	State string `json:"state,omitempty" table:"State"`
}

// NewClient accepts only the Fireworks API origin before sending a saved credential.
func NewClient(target, apiKey string) (*Client, error) {
	endpoint, err := url.Parse(target)
	if err != nil || !strings.EqualFold(endpoint.Scheme, "https") ||
		!strings.EqualFold(endpoint.Hostname(), "api.fireworks.ai") ||
		(endpoint.Port() != "" && endpoint.Port() != "443") ||
		endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" ||
		(endpoint.Path != "" && endpoint.Path != "/") {
		return nil, fmt.Errorf("Fireworks connection target must be https://api.fireworks.ai")
	}
	if strings.TrimSpace(apiKey) == "" || strings.ContainsAny(apiKey, "\r\n") {
		return nil, fmt.Errorf("Fireworks connection must contain a non-empty API key")
	}
	return &Client{
		endpoint: "https://api.fireworks.ai",
		apiKey:   apiKey,
		httpClient: &http.Client{
			Timeout:       60 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// ValidateAccount rejects empty IDs and path components before authentication.
func ValidateAccount(account string) error {
	if !resourceIDPattern.MatchString(account) {
		return fmt.Errorf("--account must be a Fireworks account ID, not an account resource path")
	}
	return nil
}

// ListModels follows opaque page tokens without following provider-supplied URLs.
func (c *Client) ListModels(ctx context.Context, account string) ([]Model, error) {
	if err := ValidateAccount(account); err != nil {
		return nil, err
	}
	models := make([]Model, 0)
	pageToken := ""
	seen := map[string]bool{"": true}
	for {
		var page struct {
			Models        []Model `json:"models"`
			NextPageToken string  `json:"nextPageToken"`
		}
		query := url.Values{}
		if pageToken != "" {
			query.Set("pageToken", pageToken)
		}
		if err := c.get(ctx, "/v1/accounts/"+account+"/models", query, &page); err != nil {
			return nil, err
		}
		models = append(models, page.Models...)
		if page.NextPageToken == "" {
			return models, nil
		}
		if seen[page.NextPageToken] {
			return nil, fmt.Errorf("Fireworks returned a repeated pagination token")
		}
		pageToken = page.NextPageToken
		seen[pageToken] = true
	}
}

func (c *Client) get(ctx context.Context, path string, query url.Values, result any) error {
	reqURL := c.endpoint + path
	if len(query) != 0 {
		reqURL += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create Fireworks request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	//nolint:gosec // The constructor restricts the endpoint to the Fireworks HTTPS API origin.
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			err = urlErr.Err
		}
		return fmt.Errorf("Fireworks request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var providerError struct {
			Message string `json:"message"`
		}
		message := http.StatusText(resp.StatusCode)
		if json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&providerError) == nil &&
			providerError.Message != "" {
			message = c.redact(providerError.Message)
		}
		return fmt.Errorf("Fireworks API error (%d): %s", resp.StatusCode, message)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4*1024*1024)).Decode(result); err != nil {
		return fmt.Errorf("failed to decode Fireworks response: %w", err)
	}
	return nil
}

func (c *Client) redact(message string) string {
	message = strings.ReplaceAll(message, c.apiKey, "[REDACTED]")
	return messageURLPattern.ReplaceAllStringFunc(message, func(value string) string {
		parsed, err := url.Parse(value)
		if err != nil {
			return "[REDACTED URL]"
		}
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.ForceQuery = false
		parsed.Fragment = ""
		return parsed.String()
	})
}
