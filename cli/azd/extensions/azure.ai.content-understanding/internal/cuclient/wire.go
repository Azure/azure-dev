// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cuclient

import (
	"bytes"
	"encoding/json"

	"time"
)

type wireAnalyzer struct {
	ID             string           `json:"analyzerId"`
	Description    string           `json:"description"`
	Status         string           `json:"status"`
	CreatedAt      time.Time        `json:"createdAt"`
	LastModifiedAt time.Time        `json:"lastModifiedAt"`
	Warnings       []wireDiagnostic `json:"warnings"`
}

type wireDiagnostic struct {
	Code    string           `json:"code"`
	Message string           `json:"message"`
	Target  string           `json:"target"`
	Details []wireDiagnostic `json:"details"`
}

func diagnostics(values []wireDiagnostic, redact bool) []Diagnostic {
	if values == nil {
		return nil
	}
	result := make([]Diagnostic, len(values))
	for i, value := range values {
		result[i] = Diagnostic{
			Code: value.Code, Message: value.Message, Target: value.Target,
			Details: diagnostics(value.Details, redact),
		}
		if redact {
			if !serviceCodePattern.MatchString(result[i].Code) {
				result[i].Code = "service_error"
			}
			result[i].Message = "The service reported an operation error."
			result[i].Target = ""
		}
	}
	return result
}

type wireUsage struct {
	DocumentPagesMinimal            *int64           `json:"documentPagesMinimal"`
	DocumentPagesBasic              *int64           `json:"documentPagesBasic"`
	DocumentPagesStandard           *int64           `json:"documentPagesStandard"`
	DocumentPagesMinimalInline      *int64           `json:"documentPagesMinimalInline"`
	DocumentPagesBasicInline        *int64           `json:"documentPagesBasicInline"`
	DocumentPagesStandardInline     *int64           `json:"documentPagesStandardInline"`
	AudioHours                      *float64         `json:"audioHours"`
	VideoHours                      *float64         `json:"videoHours"`
	ContextualizationTokens         *int64           `json:"contextualizationTokens"`
	AdvancedContextualizationTokens *int64           `json:"advancedContextualizationTokens"`
	Tokens                          map[string]int64 `json:"tokens"`
}

func (w *wireUsage) domain() *Usage {
	if w == nil {
		return nil
	}
	return &Usage{
		DocumentPagesMinimal: w.DocumentPagesMinimal, DocumentPagesBasic: w.DocumentPagesBasic,
		DocumentPagesStandard: w.DocumentPagesStandard, DocumentPagesMinimalInline: w.DocumentPagesMinimalInline,
		DocumentPagesBasicInline: w.DocumentPagesBasicInline, DocumentPagesStandardInline: w.DocumentPagesStandardInline,
		AudioHours: w.AudioHours, VideoHours: w.VideoHours, ContextualizationTokens: w.ContextualizationTokens,
		AdvancedContextualizationTokens: w.AdvancedContextualizationTokens, Tokens: w.Tokens,
	}
}

func decodeWire(body json.RawMessage, target any) error {
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '{' || json.Unmarshal(body, target) != nil {
		return malformedResponse()
	}
	return nil
}

func parseAnalyzer(body json.RawMessage, expectedID string) (Analyzer, error) {
	var wire wireAnalyzer
	if err := decodeWire(body, &wire); err != nil {
		return Analyzer{}, err
	}
	// Service-returned IDs can include legacy/internal identifiers outside request ID limits.
	if wire.ID == "" || wire.Status == "" || (expectedID != "" && wire.ID != expectedID) {
		return Analyzer{}, malformedResponse()
	}
	return Analyzer{
		ID: wire.ID, Description: wire.Description, Status: wire.Status,
		CreatedAt: wire.CreatedAt, LastModifiedAt: wire.LastModifiedAt,
		Warnings: diagnostics(wire.Warnings, false), JSON: body,
	}, nil
}

func malformedResponse() error {
	return &Error{
		Kind: ErrorService, Code: "malformed_response", Message: "The CU service returned an invalid response.",
		Suggestion: "Check the selected API version and retry. If the problem persists, contact support.",
	}
}
