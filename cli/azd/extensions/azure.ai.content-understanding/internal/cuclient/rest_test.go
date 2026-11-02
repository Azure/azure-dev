// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cuclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newRESTTransport(
	t *testing.T, apiVersion string, fn transportFunc,
) *RESTTransport {
	t.Helper()
	client, err := NewRESTTransport("https://resource.test/contentunderstanding", &testCredential{}, &TransportOptions{
		APIVersion: apiVersion,
		Transport:  fn,
	})
	require.NoError(t, err)
	return client
}

func restResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}

func TestFuturePreviewTransport(t *testing.T) {
	const version = "2027-01-01-preview"
	for _, mode := range []AnalysisMode{AnalysisModeAsync, AnalysisModeInline} {
		t.Run(string(mode), func(t *testing.T) {
			calls := 0
			client := newRESTTransport(t, version, func(request *http.Request) (*http.Response, error) {
				calls++
				assert.Equal(t, version, request.URL.Query().Get("api-version"))
				if mode == AnalysisModeAsync && calls == 1 {
					response := restResponse(request, http.StatusAccepted, `{"id":"op","status":"Running"}`)
					response.Header.Set("Operation-Location",
						"https://resource.test/contentunderstanding/analyzerResults/op")
					return response, nil
				}
				if mode == AnalysisModeAsync {
					assert.Equal(t, http.MethodGet, request.Method)
					assert.Equal(t, "/contentunderstanding/analyzerResults/op", request.URL.Path)
				} else {
					assert.Equal(t, "/contentunderstanding/analyzers/one:analyzeInline", request.URL.Path)
				}
				return restResponse(request, http.StatusOK,
					`{"id":"op","status":"Succeeded","result":{"analyzerId":"one","apiVersion":"`+version+
						`","contents":[{"kind":"document","mimeType":"text/markdown","markdown":"text"}]}}`), nil
			})
			client.pollInterval = 0
			response, err := client.Analyze(t.Context(), AnalyzeRequest{
				AnalyzerID: "one", Mode: mode, Input: Input{URL: "https://example.test/a.pdf"},
			})
			require.NoError(t, err)
			assert.Equal(t, version, response.Result.APIVersion)
			if mode == AnalysisModeAsync {
				assert.Equal(t, 2, calls)
			} else {
				assert.Equal(t, 1, calls)
			}
		})
	}
}

func TestRESTTransportContract(t *testing.T) {
	var listCalls int
	client := newRESTTransport(t, PreviewAPIVersion, func(
		request *http.Request,
	) (*http.Response, error) {
		assert.Equal(t, PreviewAPIVersion, request.URL.Query().Get("api-version"))
		assert.Equal(t, "Bearer private-access-token", request.Header.Get("Authorization"))
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/contentunderstanding/analyzers":
			listCalls++
			if listCalls == 1 {
				return restResponse(request, http.StatusOK,
					`{"value":[{"analyzerId":"one","status":"ready"}],`+
						`"nextLink":"https://resource.test/contentunderstanding/analyzers?skip=1"}`), nil
			}
			return restResponse(request, http.StatusOK,
				`{"value":[{"analyzerId":"two","status":"ready"}]}`), nil
		case request.Method == http.MethodGet && request.URL.Path == "/contentunderstanding/analyzers/one":
			return restResponse(request, http.StatusOK,
				`{"analyzerId":"one","status":"ready","description":"example","future":true}`), nil
		case request.Method == http.MethodPut && request.URL.Path == "/contentunderstanding/analyzers/created":
			assert.Equal(t, "false", request.URL.Query().Get("allowReplace"))
			return restResponse(request, http.StatusOK,
				`{"status":"Succeeded","result":{"analyzerId":"created","status":"ready"},`+
					`"usage":{"documentPagesMinimal":0}}`), nil
		case request.Method == http.MethodDelete && request.URL.Path == "/contentunderstanding/analyzers/one":
			return restResponse(request, http.StatusNoContent, ""), nil
		case request.Method == http.MethodPost &&
			request.URL.Path == "/contentunderstanding/analyzers/one:grantCopyAuthorization":
			return restResponse(request, http.StatusOK,
				`{"source":"source","targetAzureResourceId":"/target","expiresAt":"2026-10-20T12:00:00Z"}`), nil
		case request.Method == http.MethodPost &&
			request.URL.Path == "/contentunderstanding/analyzers/copied:copy":
			assert.Equal(t, "false", request.URL.Query().Get("allowReplace"))
			return restResponse(request, http.StatusOK,
				`{"status":"Succeeded","result":{"analyzerId":"copied","status":"ready"},`+
					`"usage":{"tokens":{"model":4}}}`), nil
		case request.Method == http.MethodGet && request.URL.Path == "/contentunderstanding/defaults":
			return restResponse(request, http.StatusOK,
				`{"modelDeployments":{"gpt-5.2":"deployment"},"future":true}`), nil
		case request.Method == http.MethodPatch && request.URL.Path == "/contentunderstanding/defaults":
			assert.Equal(t, "application/merge-patch+json", request.Header.Get("Content-Type"))
			return restResponse(request, http.StatusOK,
				`{"modelDeployments":{"gpt-5.2":"updated"}}`), nil
		case request.Method == http.MethodPost &&
			request.URL.Path == "/contentunderstanding/analyzers/one:analyze":
			assert.Equal(t, "codePoint", request.URL.Query().Get("stringEncoding"))
			response := restResponse(request, http.StatusAccepted,
				`{"id":"op","status":"Succeeded","result":{"analyzerId":"one",`+
					`"apiVersion":"2026-06-01-preview","contents":[{"kind":"document",`+
					`"mimeType":"text/markdown","fields":{"large":9007199254740993}}]},`+
					`"usage":{"tokens":{"model":4}},"future":9007199254740993}`)
			response.Header.Set("Operation-Location",
				"https://resource.test/contentunderstanding/analyzerResults/op")
			return response, nil
		case request.Method == http.MethodPost &&
			request.URL.Path == "/contentunderstanding/analyzers/one:analyzeBinaryInline":
			assert.Equal(t, "codePoint", request.URL.Query().Get("stringEncoding"))
			return restResponse(request, http.StatusOK,
				`{"id":"op","status":"Succeeded","result":{"analyzerId":"one",`+
					`"apiVersion":"2026-06-01-preview","contents":[{"kind":"document",`+
					`"mimeType":"text/markdown","fields":{}}]}}`), nil
		default:
			t.Fatalf("unexpected client request: %s %s", request.Method, request.URL.String())
			return nil, nil
		}
	})

	list, err := client.ListAnalyzers(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []string{"one", "two"}, []string{list[0].ID, list[1].ID})
	analyzer, err := client.GetAnalyzer(t.Context(), "one")
	require.NoError(t, err)
	assert.Contains(t, string(analyzer.JSON), `"future":true`)
	created, err := client.CreateAnalyzer(t.Context(), CreateAnalyzerRequest{
		AnalyzerID: "created", Definition: []byte(`{"description":"example"}`),
	})
	require.NoError(t, err)
	require.NotNil(t, created.Usage)
	assert.Equal(t, new(int64(0)), created.Usage.DocumentPagesMinimal)
	require.NoError(t, client.DeleteAnalyzer(t.Context(), "one"))
	authorization, err := client.GrantCopyAuthorization(t.Context(), GrantCopyAuthorizationRequest{
		AnalyzerID: "one", TargetAzureResourceID: "/target",
	})
	require.NoError(t, err)
	assert.Equal(t, "source", authorization.Source)
	copied, err := client.CopyAnalyzer(t.Context(), CopyAnalyzerRequest{
		SourceAnalyzerID: "one", DestinationAnalyzerID: "copied",
	})
	require.NoError(t, err)
	require.NotNil(t, copied.Usage)
	assert.Equal(t, map[string]int64{"model": 4}, copied.Usage.Tokens)
	defaults, err := client.GetDefaults(t.Context())
	require.NoError(t, err)
	assert.Contains(t, string(defaults.JSON), `"future":true`)
	defaults, err = client.UpdateDefaults(t.Context(), UpdateDefaultsRequest{
		ModelDeployments: map[string]string{"gpt-5.2": "updated"},
	})
	require.NoError(t, err)
	assert.Equal(t, "updated", defaults.ModelDeployments["gpt-5.2"])
	analyzed, err := client.Analyze(t.Context(), AnalyzeRequest{
		AnalyzerID: "one", Mode: AnalysisModeAsync, Input: Input{URL: "https://input.test/a"},
	})
	require.NoError(t, err)
	assert.Contains(t, string(analyzed.JSON), `"future":9007199254740993`)
	assert.Contains(t, string(analyzed.Result.Contents[0].Fields["large"]), "9007199254740993")
	binary := &ownedInput{reader: bytes.NewReader([]byte("input"))}
	_, err = client.Analyze(t.Context(), AnalyzeRequest{
		AnalyzerID: "one", Mode: AnalysisModeInline,
		Input: Input{Body: binary, ContentType: "application/pdf"},
	})
	require.NoError(t, err)
	assert.False(t, binary.closed, "the adapter must not close caller-owned input")
}

func TestRESTTransportSafeErrorsAndRetryPolicy(t *testing.T) {
	t.Run("nested service code is safe", func(t *testing.T) {
		client := newRESTTransport(t, DefaultAPIVersion, func(request *http.Request) (*http.Response, error) {
			return restResponse(request, http.StatusBadRequest,
				`{"error":{"code":"InvalidRequest","message":"private value",`+
					`"innererror":{"code":"DefaultsNotSet"}}}`), nil
		})
		_, err := client.GetDefaults(t.Context())
		var serviceErr *Error
		require.ErrorAs(t, err, &serviceErr)
		assert.Equal(t, "DefaultsNotSet", serviceErr.Code)
		assert.NotContains(t, err.Error(), "private value")
	})

	t.Run("writes are not replayed", func(t *testing.T) {
		var calls int
		client := newRESTTransport(t, DefaultAPIVersion, func(request *http.Request) (*http.Response, error) {
			calls++
			return restResponse(request, http.StatusInternalServerError,
				`{"error":{"code":"ServiceUnavailable"}}`), nil
		})
		_, err := client.CreateAnalyzer(t.Context(), CreateAnalyzerRequest{
			AnalyzerID: "created", Definition: []byte(`{"description":"example"}`),
		})
		require.Error(t, err)
		assert.Equal(t, 1, calls)
	})

	t.Run("authentication challenges do not replay writes", func(t *testing.T) {
		var calls int
		client := newRESTTransport(t, DefaultAPIVersion, func(request *http.Request) (*http.Response, error) {
			calls++
			response := restResponse(request, http.StatusUnauthorized,
				`{"error":{"code":"AuthenticationFailed"}}`)
			response.Header.Set("WWW-Authenticate",
				`Bearer error="insufficient_claims", claims="e30="`)
			return response, nil
		})
		_, err := client.CreateAnalyzer(t.Context(), CreateAnalyzerRequest{
			AnalyzerID: "created", Definition: []byte(`{"description":"example"}`),
		})
		require.Error(t, err)
		assert.Equal(t, 1, calls)
	})

	t.Run("reads retain bounded retry", func(t *testing.T) {
		var calls int
		client := newRESTTransport(t, DefaultAPIVersion, func(request *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				return restResponse(request, http.StatusInternalServerError,
					`{"error":{"code":"ServiceUnavailable"}}`), nil
			}

			return restResponse(request, http.StatusOK,
				`{"analyzerId":"one","status":"ready"}`), nil
		})
		_, err := client.GetAnalyzer(context.Background(), "one")
		require.NoError(t, err)
		assert.Equal(t, 2, calls)
	})
}

func TestRESTTransportCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client := newRESTTransport(t, DefaultAPIVersion, func(request *http.Request) (*http.Response, error) {
		return nil, request.Context().Err()
	})
	_, err := client.Analyze(ctx, AnalyzeRequest{
		AnalyzerID: "one", Mode: AnalysisModeAsync, Input: Input{URL: "https://input.test/a"},
	})
	var domain *Error
	require.ErrorAs(t, err, &domain)
	assert.Equal(t, ErrorCancelled, domain.Kind)
	assert.True(t, errors.Is(err, context.Canceled))
}

func TestRESTTransportTerminalOperationErrors(t *testing.T) {
	for _, mode := range []string{"analysis", "inline", "create", "copy"} {
		for _, tc := range []struct {
			status string
			code   string
			kind   ErrorKind
			suffix string
		}{
			{"Failed", "", ErrorService, "failed"},
			{"Canceled", "", ErrorCancelled, "canceled"},
			{"Cancelled", "", ErrorCancelled, "canceled"},
			{"Failed", "InvalidContent", ErrorService, "failed"},
			{"Canceled", "OperationCanceled", ErrorCancelled, "canceled"},
			{"Failed", "private value", ErrorService, "failed"},
		} {
			for _, polled := range []bool{false, true} {
				if mode == "inline" && polled {
					continue
				}
				t.Run(fmt.Sprintf("%s/%s/%s/polled=%t", mode, tc.status, tc.code, polled), func(t *testing.T) {
					var calls int
					client := newRESTTransport(t, PreviewAPIVersion, func(request *http.Request) (*http.Response, error) {
						calls++
						if polled && calls == 1 {
							status := http.StatusOK
							if mode == "analysis" {
								status = http.StatusAccepted
							}
							response := restResponse(request, status, `{"status":"Running"}`)
							location := "https://resource.test/contentunderstanding/analyzers/one/operations/op"
							if mode == "analysis" {
								location = "https://resource.test/contentunderstanding/analyzerResults/op"
							}
							response.Header.Set("Operation-Location", location)
							return response, nil
						}
						payload := fmt.Sprintf(`{"status":%q,"error":{"code":%q,"message":"private value",`+
							`"details":[{"code":"InvalidField","message":"private value","target":"private target",`+
							`"details":[{"code":"private value","message":"private value"}]}]}}`,
							tc.status, tc.code)
						status := http.StatusOK
						if mode == "analysis" && !polled {
							status = http.StatusAccepted
						}
						response := restResponse(request, status, payload)
						if mode == "analysis" && !polled {
							response.Header.Set("Operation-Location",
								"https://resource.test/contentunderstanding/analyzerResults/op")
						}
						return response, nil
					})
					var err error
					operation := "analyzer"
					switch mode {
					case "analysis", "inline":
						operation = "analysis"
						analysisMode := AnalysisModeAsync
						if mode == "inline" {
							analysisMode = AnalysisModeInline
						}
						_, err = client.Analyze(t.Context(), AnalyzeRequest{
							AnalyzerID: "one", Mode: analysisMode, Input: Input{URL: "https://input.test/a"},
						})
					case "create":
						_, err = client.CreateAnalyzer(t.Context(), CreateAnalyzerRequest{
							AnalyzerID: "one", Definition: []byte(`{"description":"example"}`),
						})
					case "copy":
						_, err = client.CopyAnalyzer(t.Context(), CopyAnalyzerRequest{
							SourceAnalyzerID: "source", DestinationAnalyzerID: "one",
						})
					}
					var domain *Error
					require.ErrorAs(t, err, &domain)
					assert.Equal(t, tc.kind, domain.Kind)
					expectedStatus := http.StatusOK
					if mode == "analysis" && !polled {
						expectedStatus = http.StatusAccepted
					}
					assert.Equal(t, expectedStatus, domain.StatusCode)
					expectedCode := operation + "_operation_" + tc.suffix
					if tc.code != "" && tc.code != "private value" {
						expectedCode = tc.code
					}
					assert.Equal(t, expectedCode, domain.Code)
					assert.Contains(t, domain.Message, operation+" operation")
					assert.NotContains(t, domain.Message, "HTTP 200")
					assert.NotContains(t, err.Error(), "private value")
					assert.Equal(t, []Diagnostic{{
						Code: "InvalidField", Message: "The service reported an operation error.",
						Details: []Diagnostic{{Code: "service_error", Message: "The service reported an operation error."}},
					}}, domain.Details)
					expectedCalls := 1
					if polled {
						expectedCalls = 2
					}
					assert.Equal(t, expectedCalls, calls)
				})
			}
		}
	}
}
