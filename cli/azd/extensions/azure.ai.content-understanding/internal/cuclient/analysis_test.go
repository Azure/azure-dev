// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cuclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/log"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const privateInputURL = "https://storage.test/input.pdf?sig=private-input-signature&sp=r"

var binaryInput = []byte("%PDF-1.7\n\x00\xffprivate-binary-input\n")

type ownedInput struct {
	reader *bytes.Reader
	closed bool
	reads  int
	seeks  int
}

func newOwnedInput() *ownedInput { return &ownedInput{reader: bytes.NewReader(binaryInput)} }

func (r *ownedInput) Read(p []byte) (int, error) { r.reads++; return r.reader.Read(p) }
func (r *ownedInput) Seek(offset int64, whence int) (int64, error) {
	r.seeks++
	return r.reader.Seek(offset, whence)
}
func (r *ownedInput) Close() error { r.closed = true; return nil }

func analysisResultJSON(version string) string {
	infos := ""
	if version == PreviewAPIVersion {
		infos = `"infos":[{"code":"LLMStats","message":"Diagnostic info","target":"contents/0"}],`
	}
	return `{
		"analyzerId":"prebuilt-layout","apiVersion":"` + version + `","createdAt":"2026-07-01T18:46:36.244Z",
		"warnings":[{"code":"InputExceededLimit","message":"Only part of the input was analyzed","target":"inputs/0",
			"details":[{"code":"PageLimit","message":"Retain this detail","target":"contents/0/endPageNumber"}]}],
		` + infos + `
		"contents":[
			{"kind":"document","mimeType":"application/pdf","markdown":"# Example\n\nContent",
				"fields":{"Total":{"type":"number","valueNumber":9007199254740993},
					"Custom":{"type":"object","valueObject":{"Nested":{"type":"string","valueString":"dynamic"}}}},
				"startPageNumber":3,"endPageNumber":7,"pages":[{"pageNumber":3,"width":8.5}],
				"serviceOnly":{"preserve":true}},
			{"kind":"audioVisual","mimeType":"video/mp4","markdown":"Audio/video transcript",
				"segments":[{"startTimeMs":0,"endTimeMs":1234,"text":"Keep timestamps"}]},
			{"kind":"futureContent","mimeType":"application/future","futureData":{"n":9007199254740993}}
		],"serviceOnly":{"dynamic":[1,2,3]}
	}`
}

func completedAnalysis(version string, mode AnalysisMode) string {
	id := `"id":"op1",`
	counter := "documentPagesStandard"
	if mode == AnalysisModeInline {
		id = ""
		counter += "Inline"
	}
	return `{` + id + `"status":"Succeeded","result":` + analysisResultJSON(version) + `,
		"usage":{"` + counter + `":5,"documentPagesMinimal":0,"tokens":{"gpt-4.1-input":123}},
		"futureEnvelope":{"n":9007199254740993}}`
}

func analysisSubmission(mode AnalysisMode, binary bool) (AnalyzeRequest, wireStep, *ownedInput) {
	request := AnalyzeRequest{AnalyzerID: "prebuilt-layout", Mode: mode, Input: Input{URL: privateInputURL}}
	step := wireStep{
		method: http.MethodPost, path: "/analyzers/prebuilt-layout:analyze", contentType: "application/json",
		body:  `{"inputs":[{"url":"` + privateInputURL + `"}]}`,
		query: url.Values{"stringEncoding": {"codePoint"}},
	}
	var reader *ownedInput
	if binary {
		reader = newOwnedInput()
		request.Input = Input{Body: reader}
		step.path += "Binary"
		step.contentType = "application/octet-stream"
		step.body = ""
		step.binary = binaryInput
	}
	if mode == AnalysisModeInline {
		step.path += "Inline"
		step.status = http.StatusOK
	} else {
		step.status = http.StatusAccepted
		step.response = `{"id":"op1","status":"NotStarted"}`
		step.headers = http.Header{
			"Operation-Location": {"https://resource.test/contentunderstanding/analyzerResults/op1"},
		}
	}
	return request, step, reader
}

func assertAnalysisResult(t *testing.T, result AnalyzeResponse, version string, mode AnalysisMode) {
	t.Helper()
	assert.Equal(t, "prebuilt-layout", result.Result.AnalyzerID)
	assert.Equal(t, version, result.Result.APIVersion)
	assert.Equal(t, time.Date(2026, 7, 1, 18, 46, 36, 244000000, time.UTC), result.Result.CreatedAt)
	assert.JSONEq(t, analysisResultJSON(version), string(result.Result.JSON))
	assert.Equal(t, completedAnalysis(version, mode), string(result.JSON), "full response must not be reconstructed")
	require.Len(t, result.Result.Contents, 3)
	assert.Equal(t, "document", result.Result.Contents[0].Kind)
	assert.Equal(t, "# Example\n\nContent", result.Result.Contents[0].Markdown)
	assert.JSONEq(t, `{"type":"number","valueNumber":9007199254740993}`,
		string(result.Result.Contents[0].Fields["Total"]))
	assert.Contains(t, string(result.Result.Contents[0].Fields["Total"]), "9007199254740993")
	assert.Contains(t, string(result.Result.Contents[0].JSON), `"endPageNumber":7`)
	assert.Contains(t, string(result.Result.Contents[1].JSON), `"endTimeMs":1234`)
	assert.Equal(t, "futureContent", result.Result.Contents[2].Kind)
	assert.Contains(t, string(result.Result.Contents[2].JSON), "9007199254740993")
	require.Len(t, result.Result.Warnings, 1)
	assert.Equal(t, Diagnostic{
		Code: "InputExceededLimit", Message: "Only part of the input was analyzed", Target: "inputs/0",
		Details: []Diagnostic{{
			Code: "PageLimit", Message: "Retain this detail", Target: "contents/0/endPageNumber",
		}},
	}, result.Result.Warnings[0])
	if version == PreviewAPIVersion {
		assert.Equal(t, []Diagnostic{{
			Code: "LLMStats", Message: "Diagnostic info", Target: "contents/0",
		}}, result.Result.Infos)
	} else {
		assert.Nil(t, result.Result.Infos)
	}
	require.NotNil(t, result.Usage)
	assert.Equal(t, new(int64(0)), result.Usage.DocumentPagesMinimal)
	assert.Nil(t, result.Usage.DocumentPagesBasic)
	assert.Equal(t, map[string]int64{"gpt-4.1-input": 123}, result.Usage.Tokens)
	if mode == AnalysisModeInline {
		assert.Equal(t, new(int64(5)), result.Usage.DocumentPagesStandardInline)
		assert.Nil(t, result.Usage.DocumentPagesStandard)
	} else {
		assert.Equal(t, new(int64(5)), result.Usage.DocumentPagesStandard)
		assert.Nil(t, result.Usage.DocumentPagesStandardInline)
	}
}

func TestAnalysisSubmissionWireMatrix(t *testing.T) {
	for _, version := range []string{DefaultAPIVersion, PreviewAPIVersion} {
		for _, mode := range []AnalysisMode{AnalysisModeAsync, AnalysisModeInline} {
			if version == DefaultAPIVersion && mode == AnalysisModeInline {
				continue
			}
			for _, binary := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/binary_%t", version, mode, binary), func(t *testing.T) {
					request, submit, reader := analysisSubmission(mode, binary)
					terminal := completedAnalysis(version, mode)
					steps := []wireStep{submit}
					if mode == AnalysisModeAsync {
						steps = append(steps,
							wireStep{
								method: http.MethodGet, path: "/analyzerResults/op1",
								response: `{"id":"op1","status":"Running"}`,
							},
							wireStep{method: http.MethodGet, path: "/analyzerResults/op1", response: terminal},
						)
					} else {
						steps[0].response = terminal
						steps[0].headers = http.Header{"Operation-Location": {"https://other.test/must-not-be-followed"}}
					}
					if reader != nil {
						_, err := reader.Seek(3, io.SeekStart)
						require.NoError(t, err)
					}
					client := newWireClient(t, version, steps...)
					result, err := client.Analyze(t.Context(), request)
					require.NoError(t, err)
					assertAnalysisResult(t, result, version, mode)
					if reader != nil {
						assert.False(t, reader.closed, "caller owns the input stream")
						assert.Positive(t, reader.reads)
					}
				})
			}
		}
	}
}

func TestAnalysisContentType(t *testing.T) {
	for _, binary := range []bool{false, true} {
		t.Run(fmt.Sprintf("binary_%t", binary), func(t *testing.T) {
			request, step, reader := analysisSubmission(AnalysisModeInline, binary)
			request.Input.ContentType = "application/pdf"
			if binary {
				step.contentType = request.Input.ContentType
			} else {
				step.body = `{"inputs":[{"url":"` + privateInputURL + `","mimeType":"application/pdf"}]}`
			}
			step.response = `{"status":"Succeeded","result":{"contents":[]}}`
			client := newWireClient(t, PreviewAPIVersion, step)
			result, err := client.Analyze(t.Context(), request)
			require.NoError(t, err)
			assert.Empty(t, result.Result.Contents)
			assert.Nil(t, result.Usage)
			assert.Empty(t, result.Result.AnalyzerID, "optional service fields are not synthesized")
			if reader != nil {
				assert.False(t, reader.closed)
			}
		})
	}
}

func TestAnalysisValidationBeforeSubmission(t *testing.T) {
	for _, binary := range []bool{false, true} {
		request, _, reader := analysisSubmission(AnalysisModeInline, binary)
		client := newWireClient(t, DefaultAPIVersion)
		result, err := client.Analyze(t.Context(), request)
		assert.Empty(t, result)
		domain, ok := errors.AsType[*Error](err)
		require.True(t, ok)
		assert.Equal(t, ErrorValidation, domain.Kind)
		assert.Equal(t, "inline_requires_preview", domain.Code)
		if reader != nil {
			assert.Zero(t, reader.reads)
			assert.Zero(t, reader.seeks)
			assert.False(t, reader.closed)
		}
	}
	for _, request := range []AnalyzeRequest{
		{},
		{AnalyzerID: "prebuilt-layout", Mode: AnalysisModeAsync},
		{AnalyzerID: "../private-token", Mode: AnalysisModeAsync, Input: Input{URL: privateInputURL}},
		{AnalyzerID: "prebuilt-layout", Mode: "unknown", Input: Input{URL: privateInputURL}},
		{AnalyzerID: "prebuilt-layout", Mode: AnalysisModeAsync, Input: Input{URL: "http://storage.test/private-token"}},
		{AnalyzerID: "prebuilt-layout", Mode: AnalysisModeAsync, Input: Input{URL: privateInputURL, Body: newOwnedInput()}},
	} {
		client := newWireClient(t, PreviewAPIVersion)
		_, err := client.Analyze(t.Context(), request)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "private-token")
	}
	for _, contentType := range []string{"invalid", " ", "application/", "application/pdf\r\nprivate-token"} {
		for _, binary := range []bool{false, true} {
			request, _, reader := analysisSubmission(AnalysisModeAsync, binary)
			request.Input.ContentType = contentType
			client := newWireClient(t, PreviewAPIVersion)
			_, err := client.Analyze(t.Context(), request)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "private-token")
			if reader != nil {
				assert.Zero(t, reader.reads)
				assert.Zero(t, reader.seeks)
			}
		}
	}
}

func TestAnalysisSubmissionsAreNotReplayed(t *testing.T) {
	for _, version := range []string{DefaultAPIVersion, PreviewAPIVersion} {
		for _, mode := range []AnalysisMode{AnalysisModeAsync, AnalysisModeInline} {
			if version == DefaultAPIVersion && mode == AnalysisModeInline {
				continue
			}
			for _, binary := range []bool{false, true} {
				for _, status := range []int{401, 403, 413, 429, 503} {
					t.Run(fmt.Sprintf("%s/%s/binary_%t/%d", version, mode, binary, status), func(t *testing.T) {
						request, step, reader := analysisSubmission(mode, binary)
						step.status = status
						step.response = `{"error":{"code":"ServiceCode","message":"private-service-error"}}`
						step.headers = http.Header{
							"Retry-After":      {"1"},
							"Www-Authenticate": {`Bearer error="insufficient_claims", claims="private-claims"`},
						}
						client := newWireClient(t, version, step)
						ctx := policy.WithRetryOptions(t.Context(), policy.RetryOptions{MaxRetries: 10})
						result, err := client.Analyze(ctx, request)
						assert.Empty(t, result)
						domain, ok := errors.AsType[*Error](err)
						require.True(t, ok)
						assert.Equal(t, status, domain.StatusCode)
						assert.Equal(t, "ServiceCode", domain.Code)
						assert.NotContains(t, err.Error(), "private-service-error")
						if reader != nil {
							assert.False(t, reader.closed)
						}
					})
				}
			}
		}
	}
}

func TestAnalysisMalformedSubmissionResponses(t *testing.T) {
	for _, mode := range []AnalysisMode{AnalysisModeAsync, AnalysisModeInline} {
		for _, body := range []string{
			"", "{", "null", "[]", `{}`, `{"status":"FutureStatus"}`, `{"status":"Succeeded"}`,
			`{"status":"Succeeded","result":null}`, `{"status":"Succeeded","result":{}}`,
			`{"status":"Succeeded","result":{"contents":null}}`, `{"status":"Succeeded","result":{"contents":"invalid"}}`,
			`{"status":"Succeeded","result":{"contents":[null]}}`, `{"status":"Succeeded","result":{"contents":[{}]}}`,
			`{"status":"Succeeded","result":{"contents":[{"kind":"document","mimeType":7}]}}`,
			`{"status":"Succeeded","result":{"contents":[{"kind":"document","mimeType":"application/pdf","fields":[]}]}}`,
			`{"status":"Succeeded","result":{"contents":[],"analyzerId":"other"}}`,
			`{"status":"Succeeded","result":{"contents":[],"apiVersion":"other"}}`,
			`{"status":"Succeeded","result":{"contents":[],"createdAt":"invalid"}}`,
			`{"status":"Succeeded","result":{"contents":[],"warnings":"private-token"}}`,
			`{"status":"Succeeded","result":{"contents":[],"infos":false}}`,
			`{"status":"Succeeded","result":{"contents":[]},"usage":{"tokens":{"model":"private-token"}}}`,
			`{"status":"Succeeded","result":{"contents":[]}} {}`,
		} {
			t.Run(string(mode)+"/"+body, func(t *testing.T) {
				request, step, _ := analysisSubmission(mode, false)
				step.response = body
				client := newWireClient(t, PreviewAPIVersion, step)
				result, err := client.Analyze(t.Context(), request)
				assert.Empty(t, result)
				domain, ok := errors.AsType[*Error](err)
				require.True(t, ok)
				assert.Equal(t, "malformed_response", domain.Code)
				assert.NotContains(t, err.Error(), "private-token")
			})
		}
	}
}

func TestAnalysisInlineRejectsPendingAndWrongHTTPStatus(t *testing.T) {
	for _, body := range []string{`{"status":"NotStarted"}`, `{"status":"Running"}`} {
		request, step, _ := analysisSubmission(AnalysisModeInline, false)
		step.response = body
		client := newWireClient(t, PreviewAPIVersion, step)
		_, err := client.Analyze(t.Context(), request)
		require.Error(t, err)
	}

	for _, mode := range []AnalysisMode{AnalysisModeAsync, AnalysisModeInline} {
		request, step, _ := analysisSubmission(mode, false)
		step.status = http.StatusOK
		if mode == AnalysisModeInline {
			step.status = http.StatusAccepted
		}
		step.response = completedAnalysis(PreviewAPIVersion, mode)
		client := newWireClient(t, PreviewAPIVersion, step)
		_, err := client.Analyze(t.Context(), request)
		require.Error(t, err, "do not silently switch between inline and async")
	}
}

func TestAnalysisInlineWithoutMIMEType(t *testing.T) {
	const body = `{"status":"Succeeded","result":{"contents":[{"kind":"document","markdown":"# Invoice",` +
		`"startPageNumber":1,"endPageNumber":1}]},"usage":{"documentPagesStandardInline":1}}`
	for _, binary := range []bool{false, true} {
		t.Run(fmt.Sprintf("binary_%t", binary), func(t *testing.T) {
			request, step, _ := analysisSubmission(AnalysisModeInline, binary)
			step.response = body
			client := newWireClient(t, PreviewAPIVersion, step)
			result, err := client.Analyze(t.Context(), request)
			require.NoError(t, err)
			require.Len(t, result.Result.Contents, 1)
			assert.Equal(t, "document", result.Result.Contents[0].Kind)
			assert.Equal(t, "# Invoice", result.Result.Contents[0].Markdown)
			assert.NotContains(t, string(result.Result.Contents[0].JSON), "mimeType")
			assert.Equal(t, body, string(result.JSON))
			require.NotNil(t, result.Usage)
			assert.Equal(t, new(int64(1)), result.Usage.DocumentPagesStandardInline)
		})
	}
}

func TestAnalysisRejectsUnsafeOperationLocation(t *testing.T) {
	for _, location := range []string{
		"", "https://other.test/contentunderstanding/analyzerResults/op1?sig=private-token",
		"http://resource.test/contentunderstanding/analyzerResults/op1",
		"https://user:private-token@resource.test/contentunderstanding/analyzerResults/op1",
		"/contentunderstanding/analyzers/prebuilt-layout/operations/op1", "/outside/op1",
		"/contentunderstanding/analyzerResults/", "/contentunderstanding/analyzerResults/op1/more",
		"/contentunderstanding/analyzerResults/other", "/contentunderstanding/analyzerResults/..",
		"/contentunderstanding/analyzerResults/%2e%2e", "/contentunderstanding/analyzerResults/op1#private-token",
		"/contentunderstanding/analyzerResults/op1?api-version=other",
		"/contentunderstanding/analyzerResults/op1?api-version=2025-11-01&api-version=2025-11-01",
	} {
		t.Run(location, func(t *testing.T) {
			request, step, _ := analysisSubmission(AnalysisModeAsync, false)
			step.headers.Set("Operation-Location", location)
			client := newWireClient(t, DefaultAPIVersion, step)
			_, err := client.Analyze(t.Context(), request)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "private-token")
		})
	}
}

func TestAnalysisRelativeOperationLocation(t *testing.T) {
	for _, location := range []string{
		"/contentunderstanding/analyzerResults/op1?api-version=" + DefaultAPIVersion,
		"https://RESOURCE.test/contentunderstanding/analyzerResults/op1",
	} {
		request, step, _ := analysisSubmission(AnalysisModeAsync, false)
		step.headers.Set("Operation-Location", location)
		client := newWireClient(t, DefaultAPIVersion,
			step,
			wireStep{
				method: http.MethodGet, path: "/analyzerResults/op1",
				response: completedAnalysis(DefaultAPIVersion, AnalysisModeAsync),
			},
		)
		result, err := client.Analyze(t.Context(), request)
		require.NoError(t, err)
		assertAnalysisResult(t, result, DefaultAPIVersion, AnalysisModeAsync)
	}
}

func TestAnalysisTerminalFailures(t *testing.T) {
	for _, mode := range []AnalysisMode{AnalysisModeAsync, AnalysisModeInline} {
		for _, state := range []string{"Failed", "Canceled", "cancelled"} {
			t.Run(string(mode)+"/"+state, func(t *testing.T) {
				request, submit, _ := analysisSubmission(mode, false)
				body := `{"status":"` + state + `","error":{"code":"OperationCode","message":"private-token",
					"target":"private-token","details":[{"code":"InvalidField","message":"private-token",
						"target":"private-token"}]}}`
				steps := []wireStep{submit}
				if mode == AnalysisModeAsync {
					steps = append(steps, wireStep{method: http.MethodGet, path: "/analyzerResults/op1", response: body})
				} else {
					steps[0].response = body
				}
				client := newWireClient(t, PreviewAPIVersion, steps...)
				result, err := client.Analyze(t.Context(), request)
				assert.Empty(t, result)
				domain, ok := errors.AsType[*Error](err)
				require.True(t, ok)
				assert.Equal(t, "OperationCode", domain.Code)
				assert.NotContains(t, fmt.Sprintf("%+v", domain.Details), "private-token")
				assert.NotContains(t, domain.Message, "private-token")
				assert.Nil(t, domain.Cause)
				if state == "Failed" {
					assert.Equal(t, ErrorService, domain.Kind)
				} else {
					assert.Equal(t, ErrorCancelled, domain.Kind)
					assert.ErrorIs(t, err, context.Canceled)
				}
			})
		}
	}
}

func TestAnalysisCompletedAtSubmission(t *testing.T) {
	for _, status := range []string{"Succeeded", "Failed", "Canceled"} {
		t.Run(status, func(t *testing.T) {
			request, step, _ := analysisSubmission(AnalysisModeAsync, false)
			step.response = completedAnalysis(DefaultAPIVersion, AnalysisModeAsync)
			if status != "Succeeded" {
				step.response = `{"id":"op1","status":"` + status + `"}`
			}
			client := newWireClient(t, DefaultAPIVersion, step)
			result, err := client.Analyze(t.Context(), request)
			if status == "Succeeded" {
				require.NoError(t, err)
				assertAnalysisResult(t, result, DefaultAPIVersion, AnalysisModeAsync)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestAnalysisOptionalUsage(t *testing.T) {
	for _, suffix := range []string{"", `,"usage":null`, `,"usage":{}`} {
		request, step, _ := analysisSubmission(AnalysisModeInline, false)
		step.response = `{"status":"Succeeded","result":{"contents":[]}` + suffix + `}`
		client := newWireClient(t, PreviewAPIVersion, step)
		result, err := client.Analyze(t.Context(), request)
		require.NoError(t, err)
		if suffix == `,"usage":{}` {
			require.NotNil(t, result.Usage)
			assert.Nil(t, result.Usage.DocumentPagesMinimal)
		} else {
			assert.Nil(t, result.Usage)
		}
	}
}

func TestAnalysisPollsRetryReadsOnly(t *testing.T) {
	request, step, _ := analysisSubmission(AnalysisModeAsync, false)
	client := newWireClient(t, DefaultAPIVersion,
		step,
		wireStep{
			method: http.MethodGet, path: "/analyzerResults/op1", status: http.StatusServiceUnavailable,
			headers: http.Header{"X-Ms-Retry-After-Ms": {"1"}},
		},
		wireStep{
			method: http.MethodGet, path: "/analyzerResults/op1",
			response: completedAnalysis(DefaultAPIVersion, AnalysisModeAsync),
		},
	)
	result, err := client.Analyze(t.Context(), request)
	require.NoError(t, err)
	assertAnalysisResult(t, result, DefaultAPIVersion, AnalysisModeAsync)
}

func TestAnalysisPollingFailures(t *testing.T) {
	for _, step := range []wireStep{
		{status: http.StatusTooManyRequests, headers: http.Header{"Retry-After": {"86400"}}},
		{status: http.StatusForbidden},
		{status: http.StatusNoContent},
		{response: `{"status":"FutureState"}`},
		{response: `{"status":"Succeeded"}`},
		{response: `{"status":"Succeeded","result":{"contents":null}}`},
		{response: `{"id":"different-operation","status":"Succeeded","result":{"contents":[]}}`},
		{response: `{"id":"different-operation","status":"Running"}`},
		{response: `{"status":"Succeeded","result":{"contents":[]},"usage":{"tokens":{"model":"invalid"}}}`},
		{response: "null"},
	} {
		t.Run(fmt.Sprintf("%d/%s", step.status, step.response), func(t *testing.T) {
			request, submit, _ := analysisSubmission(AnalysisModeAsync, false)
			step.method = http.MethodGet
			step.path = "/analyzerResults/op1"
			client := newWireClient(t, DefaultAPIVersion, submit, step)
			result, err := client.Analyze(t.Context(), request)
			assert.Empty(t, result)
			require.Error(t, err)
			if step.status == http.StatusTooManyRequests {
				domain, ok := errors.AsType[*Error](err)
				require.True(t, ok)
				assert.Equal(t, ErrorThrottled, domain.Kind)
				assert.Equal(t, 24*time.Hour, domain.RetryAfter)
			}
		})
	}
}

func TestAnalysisRetryAfterHonorsDeadline(t *testing.T) {
	for _, pendingPoll := range []bool{false, true} {
		t.Run(fmt.Sprintf("pending_poll_%t", pendingPoll), func(t *testing.T) {
			request, submit, _ := analysisSubmission(AnalysisModeAsync, false)
			steps := []wireStep{submit}
			if pendingPoll {
				steps = append(steps, wireStep{
					method: http.MethodGet, path: "/analyzerResults/op1", response: `{"id":"op1","status":"Running"}`,
					headers: http.Header{"Retry-After": {"86400"}},
				})
			} else {
				submit.headers.Set("Retry-After", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
			}
			client := newWireClient(t, DefaultAPIVersion, steps...)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			start := time.Now()
			_, err := client.Analyze(ctx, request)
			assert.ErrorIs(t, err, context.DeadlineExceeded)
			assert.Less(t, time.Since(start), time.Second)
		})
	}
}

func TestAnalysisCancellationAndTransportFailure(t *testing.T) {
	for _, binary := range []bool{false, true} {
		request, _, reader := analysisSubmission(AnalysisModeAsync, binary)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		client := newWireClient(t, DefaultAPIVersion)
		_, err := client.Analyze(ctx, request)
		assert.ErrorIs(t, err, context.Canceled)
		if reader != nil {
			assert.Zero(t, reader.reads)
			assert.Zero(t, reader.seeks)
			assert.False(t, reader.closed)
		}
		calls := 0
		client = newRESTTransport(t, DefaultAPIVersion, func(*http.Request) (*http.Response, error) {
			calls++
			return nil, errors.New("private-network-details")
		})
		_, err = client.Analyze(t.Context(), request)
		require.Error(t, err)
		assert.Equal(t, 1, calls)
		assert.NotContains(t, err.Error(), "private-network-details")
		if reader != nil {
			assert.False(t, reader.closed)
		}
	}
}

func TestAnalysisSubmissionTimeout(t *testing.T) {
	calls := 0
	client, err := NewRESTTransport("https://resource.test", &testCredential{}, &TransportOptions{
		RequestTimeout: 10 * time.Millisecond,
		Transport: transportFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			<-request.Context().Done()
			return nil, request.Context().Err()
		}),
	})
	require.NoError(t, err)
	request, _, reader := analysisSubmission(AnalysisModeAsync, true)
	_, err = client.Analyze(t.Context(), request)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, 1, calls)
	assert.False(t, reader.closed)
}

func TestAnalysisLocalHTTPSRoundTrip(t *testing.T) {
	var submissions, polls atomic.Int32
	terminal := completedAnalysis(DefaultAPIVersion, AnalysisModeAsync)
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		assert.Equal(t, DefaultAPIVersion, request.URL.Query().Get("api-version"))
		assert.NotEmpty(t, request.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPost {
			submissions.Add(1)
			assert.Equal(t, "/contentunderstanding/analyzers/prebuilt-layout:analyzeBinary", request.URL.Path)
			assert.Equal(t, "application/octet-stream", request.Header.Get("Content-Type"))
			body, err := io.ReadAll(request.Body)
			assert.NoError(t, err)
			assert.Equal(t, binaryInput, body)
			w.Header().Set("Operation-Location", server.URL+"/contentunderstanding/analyzerResults/op1")
			w.WriteHeader(http.StatusAccepted)
			_, err = io.WriteString(w, `{"id":"op1","status":"NotStarted"}`)
			assert.NoError(t, err)
		} else {
			polls.Add(1)
			assert.Equal(t, http.MethodGet, request.Method)
			assert.Equal(t, "/contentunderstanding/analyzerResults/op1", request.URL.Path)
			_, err := io.WriteString(w, terminal)
			assert.NoError(t, err)
		}
	}))
	defer server.Close()
	client, err := NewRESTTransport(server.URL, &testCredential{}, &TransportOptions{Transport: server.Client()})
	require.NoError(t, err)
	request, _, reader := analysisSubmission(AnalysisModeAsync, true)
	result, err := client.Analyze(t.Context(), request)
	require.NoError(t, err)
	assertAnalysisResult(t, result, DefaultAPIVersion, AnalysisModeAsync)
	assert.EqualValues(t, 1, submissions.Load())
	assert.EqualValues(t, 1, polls.Load())
	assert.False(t, reader.closed)
}

func TestAnalysisClientLogsDoNotExposeInputOrResult(t *testing.T) {
	var messages []string
	log.SetListener(func(_ log.Event, message string) { messages = append(messages, message) })
	log.SetEvents(log.EventRequest, log.EventResponse)
	t.Cleanup(func() { log.SetListener(nil); log.SetEvents() })
	for _, binary := range []bool{false, true} {
		request, step, _ := analysisSubmission(AnalysisModeInline, binary)
		step.response = `{"status":"Succeeded","result":{"contents":[{"kind":"document","mimeType":"application/pdf",
			"markdown":"private-extracted-result"}]}}`
		client := newWireClient(t, PreviewAPIVersion, step)
		_, err := client.Analyze(t.Context(), request)
		require.NoError(t, err)
	}
	require.NotEmpty(t, messages)
	for _, secret := range []string{
		"private-access-token", privateInputURL, "private-input-signature",
		"private-binary-input", "private-extracted-result",
	} {
		assert.NotContains(t, strings.Join(messages, "\n"), secret)
	}
}

func TestAnalysisResponseBodyFailures(t *testing.T) {
	for _, binary := range []bool{false, true} {
		for _, body := range []io.ReadCloser{brokenBody{}, closeErrorBody{strings.NewReader(`{}`)}} {
			request, _, reader := analysisSubmission(AnalysisModeAsync, binary)
			client := newRESTTransport(t, DefaultAPIVersion, func(*http.Request) (*http.Response, error) {
				result := response(http.StatusAccepted, "")
				result.Body = body
				return result, nil
			})
			_, err := client.Analyze(t.Context(), request)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "private")
			if reader != nil {
				assert.False(t, reader.closed)
			}
		}
	}
}

type failingInput struct {
	*ownedInput
	seekErr error
	readErr error
}

func (r *failingInput) Seek(offset int64, whence int) (int64, error) {
	if r.seekErr != nil {
		return 0, r.seekErr
	}
	return r.ownedInput.Seek(offset, whence)
}

func (r *failingInput) Read(p []byte) (int, error) {
	if r.readErr != nil {
		return 0, r.readErr
	}
	return r.ownedInput.Read(p)
}

func TestAnalysisInputStreamFailures(t *testing.T) {
	for _, seekFailure := range []bool{false, true} {
		t.Run(fmt.Sprintf("seek_%t", seekFailure), func(t *testing.T) {
			reader := &failingInput{ownedInput: newOwnedInput(), readErr: errors.New("private-read-error")}
			if seekFailure {
				reader.seekErr = errors.New("private-seek-error")
			}
			calls := 0
			client := newRESTTransport(t, DefaultAPIVersion, func(request *http.Request) (*http.Response, error) {
				calls++
				_, err := io.ReadAll(request.Body)
				return nil, err
			})
			result, err := client.Analyze(t.Context(), AnalyzeRequest{
				AnalyzerID: "prebuilt-layout", Mode: AnalysisModeAsync, Input: Input{Body: reader},
			})
			assert.Empty(t, result)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "private")
			assert.False(t, reader.closed)
			if seekFailure {
				assert.Zero(t, calls)
			} else {
				assert.Equal(t, 1, calls, "input read failures must not cause resubmission")
			}
		})
	}
}

func TestAnalysisCancellationDuringPollRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	pollStarted := make(chan struct{})
	submissions, polls := 0, 0
	client := newRESTTransport(t, DefaultAPIVersion, func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodPost {
			submissions++
			result := response(http.StatusAccepted, `{"status":"NotStarted","id":"op1"}`)
			result.Header.Set("Operation-Location", "/contentunderstanding/analyzerResults/op1")
			return result, nil
		}
		polls++
		close(pollStarted)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	go func() {
		select {
		case <-pollStarted:
			cancel()
		case <-ctx.Done():
		}
	}()
	request, _, reader := analysisSubmission(AnalysisModeAsync, true)
	_, err := client.Analyze(ctx, request)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, submissions)
	assert.Equal(t, 1, polls)
	assert.False(t, reader.closed)
}

func TestAnalysisResponseBodyTimeoutOverHTTPS(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, err := io.WriteString(w, `{"status":`)
		assert.NoError(t, err)
		assert.NoError(t, http.NewResponseController(w).Flush())
		// Keep the response incomplete until the client returns; cancellation must not race a final EOF.
		<-release
	}))
	defer server.Close()
	defer close(release)
	client, err := NewRESTTransport(server.URL, &testCredential{}, &TransportOptions{
		APIVersion: PreviewAPIVersion, RequestTimeout: 100 * time.Millisecond, Transport: server.Client(),
	})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	request, _, reader := analysisSubmission(AnalysisModeInline, true)
	_, err = client.Analyze(ctx, request)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.NoError(t, ctx.Err(), "per-attempt timeout must bound body reads before the caller's overall deadline")
	assert.EqualValues(t, 1, calls.Load())
	assert.False(t, reader.closed)
}

func TestAnalysisFullJSONCanBeDecodedWithoutClientTypes(t *testing.T) {
	request, step, _ := analysisSubmission(AnalysisModeInline, false)
	step.response = completedAnalysis(PreviewAPIVersion, AnalysisModeInline)
	client := newWireClient(t, PreviewAPIVersion, step)
	result, err := client.Analyze(t.Context(), request)
	require.NoError(t, err)
	var full struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
		Usage  json.RawMessage `json:"usage"`
	}
	require.NoError(t, json.Unmarshal(result.JSON, &full))
	assert.Equal(t, "Succeeded", full.Status)
	assert.JSONEq(t, string(result.Result.JSON), string(full.Result))
	assert.Contains(t, string(full.Usage), "documentPagesStandardInline")
}
