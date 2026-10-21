// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cuclient

import (
	"context"
	"encoding/json"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
)

var _ Client = (*RESTTransport)(nil)

// Analyze submits one URL or caller-owned binary stream and returns completed
// results. Inline analysis is preview-only and never falls back to async.
func (c *RESTTransport) Analyze(ctx context.Context, request AnalyzeRequest) (AnalyzeResponse, error) {
	if err := request.Validate(); err != nil {
		return AnalyzeResponse{}, err
	}
	inline := request.Mode == AnalysisModeInline
	if inline && c.apiVersion != PreviewAPIVersion {
		return AnalyzeResponse{}, validationError(
			"inline_requires_preview", "Inline analysis requires the tested preview API version.",
		)
	}
	contentType, err := validateContentType(request.Input.ContentType)
	if err != nil {
		return AnalyzeResponse{}, err
	}
	action := ":analyze"
	if request.Input.Body != nil {
		action += "Binary"
	}
	if inline {
		action += "Inline"
	}
	path := "/analyzers/" + request.AnalyzerID + action
	var response *http.Response
	if request.Input.Body != nil {
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		response, err = c.request(ctx, http.MethodPost, path, nil,
			streaming.NopCloser(request.Input.Body), contentType)
	} else {
		payload := struct {
			Inputs []wireAnalysisInput `json:"inputs"`
		}{Inputs: []wireAnalysisInput{{URL: request.Input.URL, MIMEType: contentType}}}
		response, err = c.jsonRequest(ctx, http.MethodPost, path, nil, payload, "application/json")
	}
	if err != nil {
		return AnalyzeResponse{}, err
	}
	expectedStatus := http.StatusAccepted
	if inline {
		expectedStatus = http.StatusOK
	}
	operation, body, err := readOperation(response, expectedStatus)
	if err != nil {
		return AnalyzeResponse{}, err
	}
	pending := strings.EqualFold(operation.Status, "notstarted") || strings.EqualFold(operation.Status, "running")
	if inline {
		if pending {
			return AnalyzeResponse{}, malformedResponse()
		}
	} else {
		pollPath, query, err := c.serviceLink(response.Header.Get("Operation-Location"), path)
		if err != nil {
			return AnalyzeResponse{}, err
		}
		id, found := strings.CutPrefix(pollPath, "/analyzerResults/")
		if !found || id == "" || strings.Contains(id, "/") || (operation.ID != "" && operation.ID != id) {
			return AnalyzeResponse{}, malformedResponse()
		}
		if pending {
			operation, body, err = c.pollOperation(ctx, pollPath, query, response.Header)
			if err != nil {
				return AnalyzeResponse{}, err
			}
		}
	}
	if !strings.EqualFold(operation.Status, "succeeded") {
		return AnalyzeResponse{}, operationError("analysis", operation.Status, operation.Error)
	}
	result, err := parseAnalysisResult(operation.Result, request.AnalyzerID, c.apiVersion)
	if err != nil {
		return AnalyzeResponse{}, err
	}
	return AnalyzeResponse{Result: result, Usage: operation.Usage.domain(), JSON: body}, nil
}

func validateContentType(contentType string) (string, error) {
	if contentType == "" {
		return "", nil
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.Contains(mediaType, "/") || strings.ContainsAny(contentType, "\r\n") {
		return "", validationError("invalid_content_type", "Specify a valid input MIME type.")
	}
	return contentType, nil
}

func parseAnalysisResult(body json.RawMessage, analyzerID, apiVersion string) (AnalysisResult, error) {
	var wire struct {
		AnalyzerID string            `json:"analyzerId"`
		APIVersion string            `json:"apiVersion"`
		CreatedAt  time.Time         `json:"createdAt"`
		Contents   []json.RawMessage `json:"contents"`
		Warnings   []wireDiagnostic  `json:"warnings"`
		Infos      []wireDiagnostic  `json:"infos"`
	}
	if err := decodeWire(body, &wire); err != nil {
		return AnalysisResult{}, err
	}
	if wire.Contents == nil || (wire.AnalyzerID != "" && wire.AnalyzerID != analyzerID) ||
		(wire.APIVersion != "" && wire.APIVersion != apiVersion) {
		return AnalysisResult{}, malformedResponse()
	}
	contents := make([]Content, 0, len(wire.Contents))
	for _, raw := range wire.Contents {
		var content struct {
			Kind     string                     `json:"kind"`
			MIMEType string                     `json:"mimeType"`
			Markdown string                     `json:"markdown"`
			Fields   map[string]json.RawMessage `json:"fields"`
		}
		if err := decodeWire(raw, &content); err != nil {
			return AnalysisResult{}, err
		}
		if content.Kind == "" || content.MIMEType == "" {
			return AnalysisResult{}, malformedResponse()
		}
		contents = append(contents, Content{
			Kind: content.Kind, Markdown: content.Markdown, Fields: content.Fields, JSON: raw,
		})
	}
	return AnalysisResult{
		AnalyzerID: wire.AnalyzerID, APIVersion: wire.APIVersion, CreatedAt: wire.CreatedAt,
		Contents: contents, Warnings: diagnostics(wire.Warnings, false), Infos: diagnostics(wire.Infos, false),
		JSON: body,
	}, nil
}
