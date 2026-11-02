// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cuclient

import (
	"encoding/json"
	"mime"

	"strings"
	"time"
)

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
		if content.Kind == "" {
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
