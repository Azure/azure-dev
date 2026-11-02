// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package curestclient

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// ErrInvalidResponse marks a successful service response that does not satisfy
// the Content Understanding response contract.
var ErrInvalidResponse = errors.New("invalid Content Understanding response")

// OperationError represents an unsuccessful terminal service operation, even
// when the polling or inline HTTP response was successful.
type OperationError struct {
	Status        string
	Code          string
	Details       []OperationDiagnostic
	ResponseError *azcore.ResponseError
}

// OperationDiagnostic retains nested service codes without customer messages or targets.
type OperationDiagnostic struct {
	Code    string                `json:"code"`
	Details []OperationDiagnostic `json:"details,omitempty"`
}

func (e *OperationError) Error() string {
	return fmt.Sprintf("Content Understanding operation ended with status %s (%s)", e.Status, e.ResponseError.ErrorCode)
}

// Unwrap preserves compatibility with callers inspecting azcore.ResponseError.
func (e *OperationError) Unwrap() error {
	return e.ResponseError
}

// AnalysisInput identifies a URL or base64-encoded binary input.
// Set exactly one of URL or Data. Data is encoded as base64 by encoding/json.
type AnalysisInput struct {
	URL      string `json:"url,omitempty"`
	Data     []byte `json:"data,omitempty"`
	Name     string `json:"name,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
}

// AnalyzeRequest contains inputs to the JSON analysis endpoints.
type AnalyzeRequest struct {
	Inputs []AnalysisInput `json:"inputs"`
}

// AnalyzeResponse retains the completed service envelope without dropping unknown properties.
// Result and Usage are JSON subtrees. Use encoding/json to decode only the properties
// required by an application, without losing integer precision or polymorphic fields.
type AnalyzeResponse struct {
	ID      string
	Status  string
	Result  json.RawMessage
	Usage   json.RawMessage
	RawJSON json.RawMessage
}

// UnmarshalJSON decodes a completed successful analysis envelope.
func (r *AnalyzeResponse) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID     string          `json:"id"`
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
		Usage  json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidResponse, err)
	}
	if wire.Status != "Succeeded" || len(wire.Result) == 0 || string(wire.Result) == "null" || wire.Result[0] != '{' {
		return fmt.Errorf("%w: analysis must contain a Succeeded status and an object result", ErrInvalidResponse)
	}
	*r = AnalyzeResponse{
		ID: wire.ID, Status: wire.Status, Result: wire.Result, Usage: wire.Usage,
		RawJSON: append(json.RawMessage(nil), data...),
	}
	return nil
}

// Analyzer retains an analyzer definition and its common identifying properties.
// RawJSON includes arbitrary schemas, configuration, categories and model metadata.
type Analyzer struct {
	AnalyzerID string
	Status     string
	RawJSON    json.RawMessage
}

// UnmarshalJSON decodes an analyzer without discarding unknown properties.
func (a *Analyzer) UnmarshalJSON(data []byte) error {
	var wire struct {
		AnalyzerID string `json:"analyzerId"`
		Status     string `json:"status"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidResponse, err)
	}
	if wire.AnalyzerID == "" {
		return fmt.Errorf("%w: analyzer response is missing analyzerId", ErrInvalidResponse)
	}
	*a = Analyzer{AnalyzerID: wire.AnalyzerID, Status: wire.Status, RawJSON: append(json.RawMessage(nil), data...)}
	return nil
}

// AnalyzerResponse retains the complete terminal create or copy envelope,
// including operation-level usage. Direct, synchronously completed analyzer
// responses are also accepted. A succeeded envelope without an embedded result
// leaves Analyzer empty so the caller can retrieve the final resource.
type AnalyzerResponse struct {
	Analyzer
	Usage   json.RawMessage
	RawJSON json.RawMessage
}

// UnmarshalJSON decodes either a terminal operation envelope or a direct analyzer.
func (r *AnalyzerResponse) UnmarshalJSON(data []byte) error {
	var operation struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
		Usage  json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(data, &operation); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidResponse, err)
	}
	if operation.Status == "Succeeded" && (len(operation.Result) == 0 || string(operation.Result) == "null") {
		*r = AnalyzerResponse{
			Usage:   append(json.RawMessage(nil), operation.Usage...),
			RawJSON: append(json.RawMessage(nil), data...),
		}
		return nil
	}
	if len(operation.Result) != 0 && string(operation.Result) != "null" {
		if operation.Status != "Succeeded" {
			return fmt.Errorf("%w: analyzer operation did not succeed", ErrInvalidResponse)
		}
		var analyzer Analyzer
		if err := json.Unmarshal(operation.Result, &analyzer); err != nil {
			return err
		}
		*r = AnalyzerResponse{
			Analyzer: analyzer,
			Usage:    append(json.RawMessage(nil), operation.Usage...),
			RawJSON:  append(json.RawMessage(nil), data...),
		}
		return nil
	}
	var analyzer Analyzer
	if err := json.Unmarshal(data, &analyzer); err != nil {
		return err
	}
	*r = AnalyzerResponse{Analyzer: analyzer, RawJSON: append(json.RawMessage(nil), data...)}
	return nil
}

// ListAnalyzersResponse is one page of analyzer definitions.
type ListAnalyzersResponse struct {
	Value    []Analyzer `json:"value"`
	NextLink string     `json:"nextLink"`
}

// Defaults contains model-to-deployment mappings.
type Defaults struct {
	ModelDeployments map[string]string `json:"modelDeployments"`
	RawJSON          json.RawMessage   `json:"-"`
}

// UnmarshalJSON retains unknown default properties while exposing deployment mappings.
func (d *Defaults) UnmarshalJSON(data []byte) error {
	var wire struct {
		ModelDeployments map[string]string `json:"modelDeployments"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidResponse, err)
	}
	if wire.ModelDeployments == nil {
		return fmt.Errorf("%w: defaults response is missing modelDeployments", ErrInvalidResponse)
	}
	*d = Defaults{
		ModelDeployments: wire.ModelDeployments,
		RawJSON:          append(json.RawMessage(nil), data...),
	}
	return nil
}

// CopyAnalyzerRequest identifies the analyzer to copy.
type CopyAnalyzerRequest struct {
	SourceAnalyzerID      string `json:"sourceAnalyzerId"`
	SourceAzureResourceID string `json:"sourceAzureResourceId,omitempty"`
	SourceRegion          string `json:"sourceRegion,omitempty"`
}

// GrantCopyAuthorizationRequest identifies the destination allowed to copy an analyzer.
type GrantCopyAuthorizationRequest struct {
	TargetAzureResourceID string `json:"targetAzureResourceId"`
	TargetRegion          string `json:"targetRegion,omitempty"`
}

// CopyAuthorization is a service-side, time-limited authorization record.
type CopyAuthorization struct {
	Source                string `json:"source"`
	TargetAzureResourceID string `json:"targetAzureResourceId"`
	ExpiresAt             string `json:"expiresAt"`
}
