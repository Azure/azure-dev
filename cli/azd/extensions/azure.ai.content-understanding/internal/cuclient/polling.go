// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cuclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// serviceLink constrains server-provided URLs before forwarding credentials.
// Paths and API versions are further checked by the operation consuming the link.
func (c *RESTTransport) serviceLink(value, currentPath string) (string, url.Values, error) {
	base, err := url.Parse(c.endpoint + currentPath)
	if err != nil {
		return "", nil, malformedResponse()
	}
	link, err := url.Parse(value)
	if err != nil || value == "" || link.User != nil || strings.Contains(value, "#") ||
		link.Opaque != "" || link.RawPath != "" || strings.Contains(link.Path, "\\") {
		return "", nil, malformedResponse()
	}
	for part := range strings.SplitSeq(link.Path, "/") {
		if part == "." || part == ".." {
			return "", nil, malformedResponse()
		}
	}
	target := base.ResolveReference(link)
	prefix := "/contentunderstanding"
	if target.Scheme != "https" || !strings.EqualFold(target.Host, base.Host) ||
		!strings.HasPrefix(target.Path, prefix+"/") {
		return "", nil, malformedResponse()
	}
	query, err := url.ParseQuery(target.RawQuery)
	if err != nil {
		return "", nil, malformedResponse()
	}
	if versions, present := query["api-version"]; present && (len(versions) != 1 || versions[0] != c.apiVersion) {
		return "", nil, malformedResponse()
	}
	query.Set("api-version", c.apiVersion)
	return strings.TrimPrefix(target.Path, prefix), query, nil
}

func (c *RESTTransport) completeAnalyzer(
	ctx context.Context, initial Analyzer, headers http.Header,
) (AnalyzerResponse, error) {
	if strings.EqualFold(initial.Status, "failed") {
		return AnalyzerResponse{}, analyzerOperationError("Failed", nil)
	}
	location := headers.Get("Operation-Location")
	if location == "" {
		if !strings.EqualFold(initial.Status, "ready") {
			return AnalyzerResponse{}, malformedResponse()
		}
		return AnalyzerResponse{Analyzer: initial}, nil
	}
	path, query, err := c.serviceLink(location, "/analyzers/"+initial.ID)
	if err != nil {
		return AnalyzerResponse{}, err
	}
	prefix := "/analyzers/" + initial.ID + "/operations/"
	operationID, found := strings.CutPrefix(path, prefix)
	if !found || operationID == "" || strings.Contains(operationID, "/") {
		return AnalyzerResponse{}, malformedResponse()
	}
	delay := retryAfter(headers.Get("Retry-After"), time.Now())
	for {
		if err := waitForPoll(ctx, delay); err != nil {
			return AnalyzerResponse{}, err
		}
		response, err := c.request(ctx, http.MethodGet, path, query, nil, "")
		if err != nil {
			return AnalyzerResponse{}, err
		}
		body, err := readResponse(response, http.StatusOK)
		if err != nil {
			return AnalyzerResponse{}, err
		}
		var operation struct {
			Status string          `json:"status"`
			Result json.RawMessage `json:"result"`
			Error  *wireDiagnostic `json:"error"`
			Usage  *wireUsage      `json:"usage"`
		}
		if err := decodeWire(body, &operation); err != nil {
			return AnalyzerResponse{}, err
		}
		switch strings.ToLower(operation.Status) {
		case "succeeded":
			var analyzer Analyzer
			if len(operation.Result) == 0 || string(operation.Result) == "null" {
				analyzer, err = c.GetAnalyzer(ctx, initial.ID)
			} else {
				analyzer, err = parseAnalyzer(operation.Result, initial.ID)
			}
			if err != nil {
				return AnalyzerResponse{}, err
			}
			if strings.EqualFold(analyzer.Status, "failed") {
				return AnalyzerResponse{}, analyzerOperationError("Failed", operation.Error)
			}
			if !strings.EqualFold(analyzer.Status, "ready") {
				return AnalyzerResponse{}, malformedResponse()
			}
			return AnalyzerResponse{Analyzer: analyzer, Usage: operation.Usage.domain()}, nil
		case "failed", "canceled", "cancelled":
			return AnalyzerResponse{}, analyzerOperationError(operation.Status, operation.Error)
		case "notstarted", "running":
			delay = retryAfter(response.Header.Get("Retry-After"), time.Now())
			if delay == 0 {
				delay = c.pollInterval
			}
		default:
			return AnalyzerResponse{}, malformedResponse()
		}
	}
}

func waitForPoll(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return transportError(ctx.Err())
	case <-timer.C:
		return nil
	}
}

func analyzerOperationError(status string, problem *wireDiagnostic) error {
	result := &Error{
		Kind: ErrorService, Code: "analyzer_operation_failed", Message: "The analyzer operation failed.",
		Suggestion: "Check the analyzer definition and dependencies before retrying.",
	}
	if strings.EqualFold(status, "canceled") || strings.EqualFold(status, "cancelled") {
		result.Kind = ErrorCancelled
		result.Code = "analyzer_operation_canceled"
		result.Message = "The analyzer operation was cancelled."
	}
	if problem != nil {
		if serviceCodePattern.MatchString(problem.Code) {
			result.Code = problem.Code
		}
		result.Details = diagnostics(problem.Details, true)
	}
	return result
}
