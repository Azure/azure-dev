// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cuclient_test

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"azure.ai.content-understanding/internal/cuclient"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContractDependencyBoundary(t *testing.T) {
	for _, name := range []string{"client.go", "models.go", "errors.go"} {
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		require.NoError(t, err)
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			require.NoError(t, err)
			assert.NotContains(t, strings.Split(path, "/")[0], ".",
				"domain contract %s must not import client, host, or transport types", name)
		}
	}
	client := reflect.TypeFor[cuclient.Client]()
	require.Equal(t, 9, client.NumMethod())
	for method := range client.Methods() {
		assert.Equal(t, reflect.TypeFor[context.Context](), method.Type.In(0), method.Name)
		assert.Equal(t, reflect.TypeFor[error](), method.Type.Out(method.Type.NumOut()-1), method.Name)
	}
}

func TestAnalyzerIDValidation(t *testing.T) {
	for _, id := range []string{"prebuilt-document", "invoice_v1.2", strings.Repeat("a", 64)} {
		assert.NoError(t, cuclient.ValidateAnalyzerID(id))
	}
	for _, id := range []string{
		"", " white-space", "../analyzer", "analyzer/name", "\u6587\u4ef6", strings.Repeat("a", 65),
	} {
		assertValidationError(t, cuclient.ValidateAnalyzerID(id), "invalid_analyzer_id")
	}
}

func TestCreateAnalyzerValidation(t *testing.T) {
	for _, definition := range []string{`{}`, `{"fieldSchema":{"fields":{"custom":{"type":"json"}}}}`} {
		assert.NoError(t, (cuclient.CreateAnalyzerRequest{
			AnalyzerID: "invoice", Definition: []byte(definition),
		}).Validate())
	}
	assertValidationError(t, (cuclient.CreateAnalyzerRequest{
		Definition: []byte(`{}`),
	}).Validate(), "invalid_analyzer_id")
	for _, definition := range []string{"", "null", "[]", `"string"`, `{"invalid":}`} {
		assertValidationError(t, (cuclient.CreateAnalyzerRequest{
			AnalyzerID: "invoice", Definition: []byte(definition),
		}).Validate(), "invalid_analyzer_definition")
	}
}

func TestCopyValidation(t *testing.T) {
	for _, request := range []cuclient.CopyAnalyzerRequest{
		{SourceAnalyzerID: "source", DestinationAnalyzerID: "destination"},
		{SourceAnalyzerID: "same", DestinationAnalyzerID: "same", SourceAzureResourceID: "/source-resource"},
	} {
		assert.NoError(t, request.Validate())
	}
	for _, tt := range []struct {
		request cuclient.CopyAnalyzerRequest
		code    string
	}{
		{cuclient.CopyAnalyzerRequest{DestinationAnalyzerID: "destination"}, "invalid_analyzer_id"},
		{cuclient.CopyAnalyzerRequest{SourceAnalyzerID: "source"}, "invalid_analyzer_id"},
		{cuclient.CopyAnalyzerRequest{SourceAnalyzerID: "same", DestinationAnalyzerID: "same"}, "identical_copy_target"},
		{cuclient.CopyAnalyzerRequest{
			SourceAnalyzerID: "source", DestinationAnalyzerID: "destination", SourceRegion: "westus",
		}, "missing_copy_source"},
	} {
		assertValidationError(t, tt.request.Validate(), tt.code)
	}
	assert.NoError(t, (cuclient.GrantCopyAuthorizationRequest{
		AnalyzerID: "source", TargetAzureResourceID: "/target-resource",
	}).Validate())
	assertValidationError(t, (cuclient.GrantCopyAuthorizationRequest{}).Validate(), "invalid_analyzer_id")
	assertValidationError(t, (cuclient.GrantCopyAuthorizationRequest{
		AnalyzerID: "source", TargetAzureResourceID: " ",
	}).Validate(), "missing_copy_target")
}

func TestDefaultsValidation(t *testing.T) {
	assert.NoError(t, (cuclient.UpdateDefaultsRequest{ModelDeployments: map[string]string{}}).Validate())
	assert.NoError(t, (cuclient.UpdateDefaultsRequest{ModelDeployments: map[string]string{
		"model": "deployment",
	}}).Validate())
	assertValidationError(t, (cuclient.UpdateDefaultsRequest{}).Validate(), "missing_model_deployments")
	for _, mapping := range []map[string]string{{"": "deployment"}, {"model": " "}} {
		assertValidationError(t, (cuclient.UpdateDefaultsRequest{
			ModelDeployments: mapping,
		}).Validate(), "invalid_model_deployment")
	}
}

func TestAnalyzeValidation(t *testing.T) {
	for _, input := range []cuclient.Input{
		{URL: "https://example.test/input.pdf?sig=private-token"},
		{Body: strings.NewReader("binary content"), ContentType: "application/pdf"},
	} {
		for _, mode := range []cuclient.AnalysisMode{cuclient.AnalysisModeAsync, cuclient.AnalysisModeInline} {
			assert.NoError(t, (cuclient.AnalyzeRequest{
				AnalyzerID: "prebuilt-document", Input: input, Mode: mode,
			}).Validate())
		}
	}
	for _, tt := range []struct {
		name    string
		request cuclient.AnalyzeRequest
		code    string
	}{
		{"missing analyzer", cuclient.AnalyzeRequest{}, "invalid_analyzer_id"},
		{"unknown mode", cuclient.AnalyzeRequest{AnalyzerID: "invoice"}, "invalid_analysis_mode"},
		{"missing input", cuclient.AnalyzeRequest{
			AnalyzerID: "invoice", Mode: cuclient.AnalysisModeAsync,
		}, "invalid_analysis_input"},
		{"conflicting inputs", cuclient.AnalyzeRequest{
			AnalyzerID: "invoice", Mode: cuclient.AnalysisModeAsync,
			Input: cuclient.Input{URL: "https://example.test/input.pdf", Body: strings.NewReader("data")},
		}, "invalid_analysis_input"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assertValidationError(t, tt.request.Validate(), tt.code)
		})
	}
	for _, url := range []string{
		"http://example.test/private-token", "https:///private-token", "/local/private-token",
		"https://user:private-token@example.test/input", "https://example.test/private-token value",
		"https://example.test:65536/private-token", "https://example.test:invalid/private-token",
		"https://example.test/%private-token",
	} {
		err := (cuclient.AnalyzeRequest{
			AnalyzerID: "invoice", Mode: cuclient.AnalysisModeAsync, Input: cuclient.Input{URL: url},
		}).Validate()
		assertValidationError(t, err, "invalid_input_url")
		assert.NotContains(t, err.Error(), "private-token")
	}
}

func TestDomainErrorSafetyAndUnwrapping(t *testing.T) {
	cause := errors.New("private-response-body")
	err := &cuclient.Error{
		Kind: cuclient.ErrorService, Code: "service_failed",
		Message: "The service request failed.", Cause: cause,
	}
	assert.Equal(t, "The service request failed.", err.Error())
	assert.ErrorIs(t, err, cause)
	assert.NotContains(t, err.Error(), "private-response-body")
	assert.ErrorIs(t, &cuclient.Error{Kind: cuclient.ErrorCancelled}, context.Canceled)
	assert.ErrorIs(t, &cuclient.Error{Kind: cuclient.ErrorTimeout}, context.DeadlineExceeded)
	assert.ErrorIs(t, &cuclient.Error{Kind: cuclient.ErrorCancelled, Cause: cause}, context.Canceled)
	assert.ErrorIs(t, &cuclient.Error{Kind: cuclient.ErrorCancelled, Cause: cause}, cause)
	assert.ErrorIs(t, &cuclient.Error{Kind: cuclient.ErrorTimeout, Cause: cause}, context.DeadlineExceeded)
	assert.NotErrorIs(t, err, context.Canceled)
	assert.NotErrorIs(t, err, context.DeadlineExceeded)
	assert.Nil(t, (&cuclient.Error{Kind: cuclient.ErrorValidation}).Unwrap())
}

func TestAbsentUsageRemainsDistinctFromZero(t *testing.T) {
	var response cuclient.AnalyzeResponse
	assert.Nil(t, response.Usage)
	zero := int64(0)
	response.Usage = &cuclient.Usage{DocumentPagesBasic: &zero}
	assert.Nil(t, response.Usage.DocumentPagesStandard)
	require.NotNil(t, response.Usage.DocumentPagesBasic)
	assert.Zero(t, *response.Usage.DocumentPagesBasic)
	assert.Equal(t, "2025-11-01", cuclient.DefaultAPIVersion)
	assert.Equal(t, "2026-06-01-preview", cuclient.PreviewAPIVersion)
}

func assertValidationError(t *testing.T, err error, code string) {
	t.Helper()
	domain, ok := errors.AsType[*cuclient.Error](err)
	require.True(t, ok, "expected a typed domain error, got %v", err)
	assert.Equal(t, cuclient.ErrorValidation, domain.Kind)
	assert.Equal(t, code, domain.Code)
	assert.NotEmpty(t, domain.Message)
	assert.NotEmpty(t, domain.Suggestion)
}
