// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package cuclient defines the host-neutral contract implemented by CU service adapters.
package cuclient

import "context"

const (
	DefaultAPIVersion = "2025-11-01"
	PreviewAPIVersion = "2026-06-01-preview"
)

// Client returns completed operations, including polling where required.
// ListAnalyzers drains all pages. Callers own input readers and must keep them
// open until Analyze returns. Adapters must honor cancellation and must not
// replace existing analyzers during creation or copy.
type Client interface {
	ListAnalyzers(context.Context) ([]Analyzer, error)
	GetAnalyzer(context.Context, string) (Analyzer, error)
	CreateAnalyzer(context.Context, CreateAnalyzerRequest) (AnalyzerResponse, error)
	DeleteAnalyzer(context.Context, string) error
	GrantCopyAuthorization(context.Context, GrantCopyAuthorizationRequest) (CopyAuthorization, error)
	CopyAnalyzer(context.Context, CopyAnalyzerRequest) (AnalyzerResponse, error)
	GetDefaults(context.Context) (Defaults, error)
	UpdateDefaults(context.Context, UpdateDefaultsRequest) (Defaults, error)
	Analyze(context.Context, AnalyzeRequest) (AnalyzeResponse, error)
}
