// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cuclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func analyzerSubmission(method string) wireStep {
	path := "/analyzers/custom"
	body := `{}`
	if method == http.MethodPost {
		path += ":copy"
		body = `{"sourceAnalyzerId":"source"}`
	}
	return wireStep{
		method: method, path: path, contentType: "application/json", body: body,
		query: url.Values{"allowReplace": {"false"}}, status: http.StatusCreated,
		response: `{"analyzerId":"custom","status":"creating"}`,
		headers: http.Header{
			"Operation-Location": {
				"https://resource.test/contentunderstanding/analyzers/custom/operations/op1",
			},
		},
	}
}

func submitAnalyzer(t *testing.T, client Client, ctx context.Context, method string) (AnalyzerResponse, error) {
	t.Helper()
	if method == http.MethodPost {
		return client.CopyAnalyzer(ctx, CopyAnalyzerRequest{SourceAnalyzerID: "source", DestinationAnalyzerID: "custom"})
	}
	return client.CreateAnalyzer(ctx, CreateAnalyzerRequest{AnalyzerID: "custom", Definition: []byte(`{}`)})
}

func TestAnalyzerPollingWire(t *testing.T) {
	for _, version := range []string{DefaultAPIVersion, PreviewAPIVersion} {
		for _, method := range []string{http.MethodPut, http.MethodPost} {
			t.Run(version+"/"+method, func(t *testing.T) {
				client := newWireClient(t, version,
					analyzerSubmission(method),
					wireStep{
						method: http.MethodGet, path: "/analyzers/custom/operations/op1",
						response: `{"id":"op1","status":"NotStarted"}`,
					},
					wireStep{
						method: http.MethodGet, path: "/analyzers/custom/operations/op1",
						response: `{"id":"op1","status":"Running"}`,
					},
					wireStep{
						method: http.MethodGet, path: "/analyzers/custom/operations/op1",
						response: `{"id":"op1","status":"Succeeded","result":` + readyAnalyzer + `,
							"usage":{"documentPagesMinimal":0,"documentPagesBasic":2,"documentPagesStandard":3,
								"audioHours":0.5,"videoHours":0.25,"contextualizationTokens":4,
								"tokens":{"gpt-4.1-input":123}}}`,
					},
				)
				result, err := submitAnalyzer(t, client, t.Context(), method)
				require.NoError(t, err)
				assertReadyAnalyzer(t, result.Analyzer)
				require.NotNil(t, result.Usage)
				assert.Equal(t, new(int64(0)), result.Usage.DocumentPagesMinimal)
				assert.Equal(t, new(int64(2)), result.Usage.DocumentPagesBasic)
				assert.Equal(t, new(int64(3)), result.Usage.DocumentPagesStandard)
				assert.Equal(t, new(0.5), result.Usage.AudioHours)
				assert.Equal(t, new(0.25), result.Usage.VideoHours)
				assert.Equal(t, new(int64(4)), result.Usage.ContextualizationTokens)
				assert.Equal(t, map[string]int64{"gpt-4.1-input": 123}, result.Usage.Tokens)
				assert.Nil(t, result.Usage.DocumentPagesMinimalInline)
				assert.Nil(t, result.Usage.AdvancedContextualizationTokens)
			})
		}
	}
}

func TestAnalyzerPollingFinalResourceRead(t *testing.T) {
	for _, version := range []string{DefaultAPIVersion, PreviewAPIVersion} {
		for _, method := range []string{http.MethodPut, http.MethodPost} {
			for _, result := range []string{"", `,"result":null`} {
				for _, usage := range []string{"", `,"usage":{"documentPagesMinimal":0,"tokens":{"model":4}}`} {
					t.Run(version+"/"+method+"/"+result+"/"+usage, func(t *testing.T) {
						client := newWireClient(t, version,
							analyzerSubmission(method),
							wireStep{
								method: http.MethodGet, path: "/analyzers/custom/operations/op1",
								response: `{"id":"op1","status":"Succeeded"` + result + usage + `}`,
							},
							wireStep{method: http.MethodGet, path: "/analyzers/custom", response: readyAnalyzer},
						)
						response, err := submitAnalyzer(t, client, t.Context(), method)
						require.NoError(t, err)
						assertReadyAnalyzer(t, response.Analyzer)
						if usage == "" {
							assert.Nil(t, response.Usage)
						} else {
							require.NotNil(t, response.Usage)
							assert.Equal(t, new(int64(0)), response.Usage.DocumentPagesMinimal)
							assert.Equal(t, map[string]int64{"model": 4}, response.Usage.Tokens)
						}
					})
				}
			}
		}
	}
}

func TestAnalyzerPollingPreviewUsage(t *testing.T) {
	client := newWireClient(t, PreviewAPIVersion,
		analyzerSubmission(http.MethodPut),
		wireStep{
			method: http.MethodGet, path: "/analyzers/custom/operations/op1",
			response: `{"status":"Succeeded","result":` + readyAnalyzer + `,
				"usage":{"documentPagesMinimalInline":0,"documentPagesBasicInline":1,"documentPagesStandardInline":2,
					"advancedContextualizationTokens":3}}`,
		},
	)
	result, err := submitAnalyzer(t, client, t.Context(), http.MethodPut)
	require.NoError(t, err)
	require.NotNil(t, result.Usage)
	assert.Equal(t, new(int64(0)), result.Usage.DocumentPagesMinimalInline)
	assert.Equal(t, new(int64(1)), result.Usage.DocumentPagesBasicInline)
	assert.Equal(t, new(int64(2)), result.Usage.DocumentPagesStandardInline)
	assert.Equal(t, new(int64(3)), result.Usage.AdvancedContextualizationTokens)
	assert.Nil(t, result.Usage.DocumentPagesStandard)
}

func TestAnalyzerPollingRejectsUnsafeLocations(t *testing.T) {
	for _, location := range []string{
		"https://other.test/contentunderstanding/analyzers/custom/operations/op1?sig=private-token",
		"http://resource.test/contentunderstanding/analyzers/custom/operations/op1",
		"/contentunderstanding/analyzers/other/operations/op1", "/contentunderstanding/analyzers/custom/operations/",
		"/contentunderstanding/analyzers/custom/operations/op1/more", "/contentunderstanding/analyzers/custom",
		"/contentunderstanding/analyzerResults/op1", "/contentunderstanding/analyzers/custom/operations/..",
		"/contentunderstanding/analyzers/custom/operations/op1?api-version=other-version",
	} {
		t.Run(location, func(t *testing.T) {
			step := analyzerSubmission(http.MethodPut)
			step.headers.Set("Operation-Location", location)
			client := newWireClient(t, DefaultAPIVersion, step)
			result, err := submitAnalyzer(t, client, t.Context(), http.MethodPut)
			assert.Empty(t, result)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "private-token")
		})
	}
}

func TestAnalyzerPollingFailuresAndMalformedResponses(t *testing.T) {
	for _, test := range []struct {
		body string
		code string
		kind ErrorKind
	}{
		{`{"status":"Failed","error":{"code":"InvalidRequest","message":"private-token","target":"private-token",
			"details":[{"code":"InvalidField","message":"private-token","target":"private-token",
				"details":[{"code":"private token","message":"private-token"}]}]}}`, "InvalidRequest", ErrorService},
		{`{"status":"Failed","error":{"code":"private token"}}`, "analyzer_operation_failed", ErrorService},
		{`{"status":"Failed"}`, "analyzer_operation_failed", ErrorService},
		{`{"status":"Canceled"}`, "analyzer_operation_canceled", ErrorCancelled},
		{`{"status":"cancelled"}`, "analyzer_operation_canceled", ErrorCancelled},
		{`{}`, "malformed_response", ErrorService},
		{`{"status":"FutureStatus"}`, "malformed_response", ErrorService},
		{`{"status":"Succeeded","result":{}}`, "malformed_response", ErrorService},
		{`{"status":"Succeeded","result":{"analyzerId":"other","status":"ready"}}`, "malformed_response", ErrorService},
		{`{"status":"Succeeded","result":{"analyzerId":"custom","status":"failed"}}`,
			"analyzer_operation_failed", ErrorService},
		{`{"status":"Succeeded","result":{"analyzerId":"custom","status":"creating"}}`, "malformed_response", ErrorService},
		{`{"status":"Succeeded","usage":{"tokens":{"model":"invalid"}}}`, "malformed_response", ErrorService},
		{`null`, "malformed_response", ErrorService},
		{`{"status":`, "malformed_response", ErrorService},
	} {
		for _, method := range []string{http.MethodPut, http.MethodPost} {
			t.Run(method+"/"+test.body, func(t *testing.T) {
				client := newWireClient(t, DefaultAPIVersion,
					analyzerSubmission(method),
					wireStep{
						method: http.MethodGet, path: "/analyzers/custom/operations/op1", response: test.body,
					},
				)
				result, err := submitAnalyzer(t, client, t.Context(), method)
				assert.Empty(t, result)
				domain, ok := errors.AsType[*Error](err)
				require.True(t, ok)
				assert.Equal(t, test.code, domain.Code)
				assert.Equal(t, test.kind, domain.Kind)
				assert.NotContains(t, fmt.Sprintf("%+v", domain), "private-token")
				assert.NotContains(t, fmt.Sprintf("%+v", domain.Details), "private-token")
				if domain.Code == "InvalidRequest" {
					require.Len(t, domain.Details, 1)
					assert.Equal(t, "InvalidField", domain.Details[0].Code)
					require.Len(t, domain.Details[0].Details, 1)
					assert.Equal(t, "service_error", domain.Details[0].Details[0].Code)
				}
				if test.kind == ErrorCancelled {
					assert.ErrorIs(t, err, context.Canceled)
				}
			})
		}
	}
}

func TestAnalyzerSubmissionRequiresCompletionInformation(t *testing.T) {
	for _, test := range []struct {
		status string
		code   string
	}{
		{"creating", "malformed_response"}, {"failed", "analyzer_operation_failed"}, {"unknown", "malformed_response"},
	} {
		t.Run(test.status, func(t *testing.T) {
			step := analyzerSubmission(http.MethodPut)
			step.headers = nil
			step.response = `{"analyzerId":"custom","status":"` + test.status + `"}`
			client := newWireClient(t, DefaultAPIVersion, step)
			_, err := submitAnalyzer(t, client, t.Context(), http.MethodPut)
			domain, ok := errors.AsType[*Error](err)
			require.True(t, ok)
			assert.Equal(t, test.code, domain.Code)
		})
	}
}

func TestAnalyzerPollingRetryAfterHonorsCancellation(t *testing.T) {
	for _, test := range []struct {
		name    string
		running bool
		value   string
	}{
		{"initial_seconds", false, "86400"},
		{"initial_date", false, time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)},
		{"running_seconds", true, "86400"},
	} {
		t.Run(test.name, func(t *testing.T) {
			initial := analyzerSubmission(http.MethodPut)
			steps := []wireStep{initial}
			if test.running {
				steps = append(steps, wireStep{
					method: http.MethodGet, path: "/analyzers/custom/operations/op1", response: `{"status":"Running"}`,
					headers: http.Header{"Retry-After": {test.value}},
				})
			} else {
				initial.headers.Set("Retry-After", test.value)
			}
			client := newWireClient(t, DefaultAPIVersion, steps...)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			start := time.Now()
			_, err := submitAnalyzer(t, client, ctx, http.MethodPut)
			assert.ErrorIs(t, err, context.DeadlineExceeded)
			assert.Less(t, time.Since(start), time.Second, "cancellation must interrupt Retry-After")
		})
	}
}

func TestAnalyzerPollingHTTPFailure(t *testing.T) {
	client := newWireClient(t, DefaultAPIVersion,
		analyzerSubmission(http.MethodPut),
		wireStep{
			method: http.MethodGet, path: "/analyzers/custom/operations/op1", status: http.StatusTooManyRequests,
			headers: http.Header{"Retry-After": {"86400"}},
		},
	)
	_, err := submitAnalyzer(t, client, t.Context(), http.MethodPut)
	domain, ok := errors.AsType[*Error](err)
	require.True(t, ok)
	assert.Equal(t, ErrorThrottled, domain.Kind)
	assert.Equal(t, 24*time.Hour, domain.RetryAfter)
}

func TestAnalyzerPollingResponseFailures(t *testing.T) {
	t.Run("unexpected_poll_status", func(t *testing.T) {
		client := newWireClient(t, DefaultAPIVersion,
			analyzerSubmission(http.MethodPut),
			wireStep{
				method: http.MethodGet, path: "/analyzers/custom/operations/op1", status: http.StatusNoContent,
			},
		)
		_, err := submitAnalyzer(t, client, t.Context(), http.MethodPut)
		require.Error(t, err)
	})
	t.Run("final_get_failure", func(t *testing.T) {
		client := newWireClient(t, DefaultAPIVersion,
			analyzerSubmission(http.MethodPut),
			wireStep{
				method: http.MethodGet, path: "/analyzers/custom/operations/op1", response: `{"status":"Succeeded"}`,
			},
			wireStep{method: http.MethodGet, path: "/analyzers/custom", status: http.StatusNotFound},
		)
		_, err := submitAnalyzer(t, client, t.Context(), http.MethodPut)
		domain, ok := errors.AsType[*Error](err)
		require.True(t, ok)
		assert.Equal(t, ErrorNotFound, domain.Kind)
	})
}

func TestAnalyzerPollingRelativeLocation(t *testing.T) {
	for _, location := range []string{
		"/contentunderstanding/analyzers/custom/operations/op1?api-version=" + PreviewAPIVersion,
		"custom/operations/op1",
	} {
		t.Run(location, func(t *testing.T) {
			initial := analyzerSubmission(http.MethodPost)
			initial.headers.Set("Operation-Location", location)
			client := newWireClient(t, PreviewAPIVersion,
				initial,
				wireStep{
					method: http.MethodGet, path: "/analyzers/custom/operations/op1",
					response: `{"status":"Succeeded","result":` + strings.TrimSpace(readyAnalyzer) + `}`,
				},
			)
			result, err := submitAnalyzer(t, client, t.Context(), http.MethodPost)
			require.NoError(t, err)
			assertReadyAnalyzer(t, result.Analyzer)
		})
	}
}
