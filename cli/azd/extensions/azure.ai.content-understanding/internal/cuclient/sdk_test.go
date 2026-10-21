// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cuclient

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSDKTestTransport(
	t *testing.T, apiVersion string, fn transportFunc,
) *SDKTransport {
	t.Helper()
	client, err := NewSDKTransport("https://resource.test/contentunderstanding", &testCredential{}, &TransportOptions{
		APIVersion: apiVersion,
		Transport:  fn,
	})
	require.NoError(t, err)
	return client
}

func sdkResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}

func TestSDKTransportContract(t *testing.T) {
	var listCalls int
	client := newSDKTestTransport(t, PreviewAPIVersion, func(request *http.Request) (*http.Response, error) {
		assert.Equal(t, PreviewAPIVersion, request.URL.Query().Get("api-version"))
		assert.Equal(t, "Bearer private-access-token", request.Header.Get("Authorization"))
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/contentunderstanding/analyzers":
			listCalls++
			if listCalls == 1 {
				return sdkResponse(request, http.StatusOK,
					`{"value":[{"analyzerId":"one","status":"ready"}],`+
						`"nextLink":"https://resource.test/contentunderstanding/analyzers?skip=1"}`), nil
			}
			return sdkResponse(request, http.StatusOK,
				`{"value":[{"analyzerId":"two","status":"ready"}]}`), nil
		case request.Method == http.MethodGet && request.URL.Path == "/contentunderstanding/analyzers/one":
			return sdkResponse(request, http.StatusOK,
				`{"analyzerId":"one","status":"ready","description":"example","future":true}`), nil
		case request.Method == http.MethodPut && request.URL.Path == "/contentunderstanding/analyzers/created":
			assert.Equal(t, "false", request.URL.Query().Get("allowReplace"))
			return sdkResponse(request, http.StatusOK,
				`{"status":"Succeeded","result":{"analyzerId":"created","status":"ready"},`+
					`"usage":{"documentPagesMinimal":0}}`), nil
		case request.Method == http.MethodDelete && request.URL.Path == "/contentunderstanding/analyzers/one":
			return sdkResponse(request, http.StatusNoContent, ""), nil
		case request.Method == http.MethodPost &&
			request.URL.Path == "/contentunderstanding/analyzers/one:grantCopyAuthorization":
			return sdkResponse(request, http.StatusOK,
				`{"source":"source","targetAzureResourceId":"/target","expiresAt":"2026-10-20T12:00:00Z"}`), nil
		case request.Method == http.MethodPost &&
			request.URL.Path == "/contentunderstanding/analyzers/copied:copy":
			assert.Equal(t, "false", request.URL.Query().Get("allowReplace"))
			return sdkResponse(request, http.StatusOK,
				`{"status":"Succeeded","result":{"analyzerId":"copied","status":"ready"},`+
					`"usage":{"tokens":{"model":4}}}`), nil
		case request.Method == http.MethodGet && request.URL.Path == "/contentunderstanding/defaults":
			return sdkResponse(request, http.StatusOK,
				`{"modelDeployments":{"gpt-5.2":"deployment"},"future":true}`), nil
		case request.Method == http.MethodPatch && request.URL.Path == "/contentunderstanding/defaults":
			assert.Equal(t, "application/merge-patch+json", request.Header.Get("Content-Type"))
			return sdkResponse(request, http.StatusOK,
				`{"modelDeployments":{"gpt-5.2":"updated"}}`), nil
		case request.Method == http.MethodPost &&
			request.URL.Path == "/contentunderstanding/analyzers/one:analyze":
			assert.Equal(t, "codePoint", request.URL.Query().Get("stringEncoding"))
			return sdkResponse(request, http.StatusAccepted,
				`{"id":"op","status":"Succeeded","result":{"analyzerId":"one",`+
					`"apiVersion":"2026-06-01-preview","contents":[{"kind":"document",`+
					`"mimeType":"text/markdown","fields":{"large":9007199254740993}}]},`+
					`"usage":{"tokens":{"model":4}},"future":9007199254740993}`), nil
		case request.Method == http.MethodPost &&
			request.URL.Path == "/contentunderstanding/analyzers/one:analyzeBinaryInline":
			assert.Equal(t, "codePoint", request.URL.Query().Get("stringEncoding"))
			return sdkResponse(request, http.StatusOK,
				`{"id":"op","status":"Succeeded","result":{"analyzerId":"one",`+
					`"apiVersion":"2026-06-01-preview","contents":[{"kind":"document",`+
					`"mimeType":"text/markdown","fields":{}}]}}`), nil
		default:
			t.Fatalf("unexpected SDK request: %s %s", request.Method, request.URL.String())
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

func TestSDKTransportSafeErrorsAndRetryPolicy(t *testing.T) {
	t.Run("nested service code is safe", func(t *testing.T) {
		client := newSDKTestTransport(t, DefaultAPIVersion, func(request *http.Request) (*http.Response, error) {
			return sdkResponse(request, http.StatusBadRequest,
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
		client := newSDKTestTransport(t, DefaultAPIVersion, func(request *http.Request) (*http.Response, error) {
			calls++
			return sdkResponse(request, http.StatusInternalServerError,
				`{"error":{"code":"ServiceUnavailable"}}`), nil
		})
		_, err := client.CreateAnalyzer(t.Context(), CreateAnalyzerRequest{
			AnalyzerID: "created", Definition: []byte(`{"description":"example"}`),
		})
		require.Error(t, err)
		assert.Equal(t, 1, calls)
	})

	t.Run("reads retain bounded retry", func(t *testing.T) {
		var calls int
		client := newSDKTestTransport(t, DefaultAPIVersion, func(request *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				return sdkResponse(request, http.StatusInternalServerError,
					`{"error":{"code":"ServiceUnavailable"}}`), nil
			}
			return sdkResponse(request, http.StatusOK,
				`{"analyzerId":"one","status":"ready"}`), nil
		})
		_, err := client.GetAnalyzer(context.Background(), "one")
		require.NoError(t, err)
		assert.Equal(t, 2, calls)
	})
}
