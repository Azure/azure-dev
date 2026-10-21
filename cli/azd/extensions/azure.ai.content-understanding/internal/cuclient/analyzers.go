// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cuclient

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
)

// ListAnalyzers drains all pages in service order without filtering or sorting.
func (c *RESTTransport) ListAnalyzers(ctx context.Context) ([]Analyzer, error) {
	query := url.Values{"api-version": {c.apiVersion}}
	seen := map[string]bool{}
	items := []Analyzer{}
	for {
		key := query.Encode()
		if seen[key] {
			return nil, malformedResponse()
		}
		seen[key] = true
		response, err := c.request(ctx, http.MethodGet, "/analyzers", query, nil, "")
		if err != nil {
			return nil, err
		}
		body, err := readResponse(response, http.StatusOK)
		if err != nil {
			return nil, err
		}
		var page struct {
			Value    []json.RawMessage `json:"value"`
			NextLink string            `json:"nextLink"`
		}
		if err := decodeWire(body, &page); err != nil {
			return nil, err
		}
		if page.Value == nil {
			return nil, malformedResponse()
		}
		for _, raw := range page.Value {
			analyzer, err := parseAnalyzer(raw, "")
			if err != nil {
				return nil, err
			}
			items = append(items, analyzer)
		}
		if page.NextLink == "" {
			return items, nil
		}
		path, nextQuery, err := c.serviceLink(page.NextLink, "/analyzers")
		if err != nil {
			return nil, err
		}
		if path != "/analyzers" {
			return nil, malformedResponse()
		}
		query = nextQuery
	}
}

// GetAnalyzer reads the complete analyzer definition and typed projections.
func (c *RESTTransport) GetAnalyzer(ctx context.Context, id string) (Analyzer, error) {
	if err := ValidateAnalyzerID(id); err != nil {
		return Analyzer{}, err
	}
	response, err := c.request(ctx, http.MethodGet, "/analyzers/"+id, nil, nil, "")
	if err != nil {
		return Analyzer{}, err
	}
	body, err := readResponse(response, http.StatusOK)
	if err != nil {
		return Analyzer{}, err
	}
	return parseAnalyzer(body, id)
}

// CreateAnalyzer creates without replacing an existing analyzer and waits for completion.
func (c *RESTTransport) CreateAnalyzer(ctx context.Context, request CreateAnalyzerRequest) (AnalyzerResponse, error) {
	if err := request.Validate(); err != nil {
		return AnalyzerResponse{}, err
	}
	return c.writeAnalyzer(ctx, http.MethodPut, request.AnalyzerID, "", request.Definition)
}

// DeleteAnalyzer follows the service's idempotent DELETE semantics. Commands
// that distinguish a missing analyzer must perform an existence check first.
func (c *RESTTransport) DeleteAnalyzer(ctx context.Context, id string) error {
	if err := ValidateAnalyzerID(id); err != nil {
		return err
	}
	response, err := c.request(ctx, http.MethodDelete, "/analyzers/"+id, nil, nil, "")
	if err != nil {
		return err
	}
	_, err = readResponse(response, http.StatusNoContent)
	return err
}

// GrantCopyAuthorization creates a server-side, destination-scoped grant on the source resource.
func (c *RESTTransport) GrantCopyAuthorization(
	ctx context.Context, request GrantCopyAuthorizationRequest,
) (CopyAuthorization, error) {
	if err := request.Validate(); err != nil {
		return CopyAuthorization{}, err
	}
	payload := struct {
		TargetAzureResourceID string `json:"targetAzureResourceId"`
		TargetRegion          string `json:"targetRegion,omitempty"`
	}{request.TargetAzureResourceID, request.TargetRegion}
	response, err := c.jsonRequest(
		ctx, http.MethodPost, "/analyzers/"+request.AnalyzerID+":grantCopyAuthorization", nil, payload, "application/json",
	)
	if err != nil {
		return CopyAuthorization{}, err
	}
	body, err := readResponse(response, http.StatusOK)
	if err != nil {
		return CopyAuthorization{}, err
	}
	var wire struct {
		Source                string    `json:"source"`
		TargetAzureResourceID string    `json:"targetAzureResourceId"`
		ExpiresAt             time.Time `json:"expiresAt"`
	}
	if err := decodeWire(body, &wire); err != nil {
		return CopyAuthorization{}, err
	}
	if wire.Source == "" || wire.TargetAzureResourceID != request.TargetAzureResourceID || wire.ExpiresAt.IsZero() {
		return CopyAuthorization{}, malformedResponse()
	}
	return CopyAuthorization{
		Source: wire.Source, TargetAzureResourceID: wire.TargetAzureResourceID, ExpiresAt: wire.ExpiresAt,
	}, nil
}

// CopyAnalyzer copies into this client's resource without replacing an existing analyzer.
// Source lookup, destination preflight, and cross-resource grants belong to commands.
func (c *RESTTransport) CopyAnalyzer(ctx context.Context, request CopyAnalyzerRequest) (AnalyzerResponse, error) {
	if err := request.Validate(); err != nil {
		return AnalyzerResponse{}, err
	}
	payload := struct {
		SourceAnalyzerID      string `json:"sourceAnalyzerId"`
		SourceAzureResourceID string `json:"sourceAzureResourceId,omitempty"`
		SourceRegion          string `json:"sourceRegion,omitempty"`
	}{request.SourceAnalyzerID, request.SourceAzureResourceID, request.SourceRegion}
	return c.writeAnalyzer(ctx, http.MethodPost, request.DestinationAnalyzerID, ":copy", payload)
}

// GetDefaults reads the service's model deployment mapping.
func (c *RESTTransport) GetDefaults(ctx context.Context) (Defaults, error) {
	response, err := c.request(ctx, http.MethodGet, "/defaults", nil, nil, "")
	if err != nil {
		return Defaults{}, err
	}
	return readDefaults(response)
}

// UpdateDefaults sends a merge patch. An empty mapping does not clear existing deployments.
func (c *RESTTransport) UpdateDefaults(ctx context.Context, request UpdateDefaultsRequest) (Defaults, error) {
	if err := request.Validate(); err != nil {
		return Defaults{}, err
	}
	payload := struct {
		ModelDeployments map[string]string `json:"modelDeployments"`
	}{request.ModelDeployments}
	response, err := c.jsonRequest(ctx, http.MethodPatch, "/defaults", nil, payload, "application/merge-patch+json")
	if err != nil {
		return Defaults{}, err
	}
	return readDefaults(response)
}

func readDefaults(response *http.Response) (Defaults, error) {
	body, err := readResponse(response, http.StatusOK)
	if err != nil {
		return Defaults{}, err
	}
	var wire struct {
		ModelDeployments map[string]string `json:"modelDeployments"`
	}
	if err := decodeWire(body, &wire); err != nil {
		return Defaults{}, err
	}
	if (UpdateDefaultsRequest{ModelDeployments: wire.ModelDeployments}).Validate() != nil {
		return Defaults{}, malformedResponse()
	}
	return Defaults{ModelDeployments: wire.ModelDeployments, JSON: body}, nil
}

func (c *RESTTransport) jsonRequest(
	ctx context.Context, method, path string, query url.Values, payload any, contentType string,
) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, validationError("invalid_request_json", "The CU request cannot be encoded as JSON.")
	}
	return c.request(ctx, method, path, query, streaming.NopCloser(bytes.NewReader(body)), contentType)
}

func (c *RESTTransport) writeAnalyzer(
	ctx context.Context, method, id, action string, payload any,
) (AnalyzerResponse, error) {
	query := url.Values{"allowReplace": {"false"}}
	response, err := c.jsonRequest(ctx, method, "/analyzers/"+id+action, query, payload, "application/json")
	if err != nil {
		return AnalyzerResponse{}, err
	}
	body, err := readResponse(response, http.StatusOK, http.StatusCreated)
	if err != nil {
		return AnalyzerResponse{}, err
	}
	analyzer, err := parseAnalyzer(body, id)
	if err != nil {
		return AnalyzerResponse{}, err
	}
	return c.completeAnalyzer(ctx, analyzer, response.Header)
}
