// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package containerregistry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockhttp"
	"github.com/stretchr/testify/require"
)

type logBlobResponse struct {
	method    string
	body      string
	etag      string
	byteRange string
	ifMatch   string
	status    int
	complete  bool
	before    func()
}

func logBlobClient(t *testing.T, responses ...logBlobResponse) *blockblob.Client {
	t.Helper()

	transport := mockhttp.NewMockHttpUtil()
	next := 0
	transport.When(func(*http.Request) bool { return true }).RespondFn(
		func(request *http.Request) (*http.Response, error) {
			require.Less(t, next, len(responses), "unexpected log request: %s", request.Method)
			step := responses[next]
			next++
			require.Equal(t, step.method, request.Method, "request %d", next)
			var byteRange string
			for name, values := range request.Header {
				if strings.EqualFold(name, "x-ms-range") {
					byteRange = strings.Join(values, ",")
				}
			}
			require.Equal(t, step.byteRange, byteRange, "request %d", next)
			if step.ifMatch != "" {
				require.Equal(t, step.ifMatch, request.Header.Get("If-Match"), "request %d", next)
			}
			if step.before != nil {
				step.before()
			}

			status := step.status
			if status == 0 {
				status = http.StatusOK
				if request.Method == http.MethodGet {
					status = http.StatusPartialContent
				}
			}
			headers := http.Header{
				"Content-Length": {strconv.Itoa(len(step.body))},
				"Etag":           {step.etag},
			}
			if step.complete {
				headers.Set("x-ms-meta-complete", "true")
			}
			body := step.body
			if request.Method == http.MethodHead {
				body = ""
			}
			if status >= http.StatusBadRequest {
				headers.Set("Content-Type", "application/xml")
				headers.Set("x-ms-error-code", "TestError")
				body = "<Error><Code>TestError</Code><Message>log request failed</Message></Error>"
			}
			return &http.Response{
				StatusCode: status,
				Header:     headers,
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    request,
			}, nil
		},
	)
	t.Cleanup(func() { require.Equal(t, len(responses), next, "log requests consumed") })
	client, err := blockblob.NewClientWithNoCredential("https://logs.example.test/run.log",
		&blockblob.ClientOptions{ClientOptions: azcore.ClientOptions{
			Transport: transport,
			Retry:     policy.RetryOptions{MaxRetries: -1},
		}})
	require.NoError(t, err)
	return client
}

func TestStreamLogs_ChangingBlob(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		responses []logBlobResponse
		output    string
	}{
		{
			name: "append without replaying previous bytes",
			responses: []logBlobResponse{
				{method: http.MethodHead, body: "one\n", etag: `"1"`},
				{method: http.MethodGet, body: "one\n", byteRange: "bytes=0-3"},
				{method: http.MethodHead, body: "one\ntwo\n", etag: `"2"`},
				{method: http.MethodGet, body: "two\n", byteRange: "bytes=4-7"},
				{method: http.MethodHead, body: "one\ntwo\n", complete: true},
			},
			output: "one\ntwo\n",
		},
		{
			name: "truncated log starts from zero",
			responses: []logBlobResponse{
				{method: http.MethodHead, body: "old attempt\n", etag: `"1"`},
				{method: http.MethodGet, body: "old attempt\n", byteRange: "bytes=0-11"},
				{method: http.MethodHead, body: "new\n", etag: `"2"`},
				{method: http.MethodGet, body: "new\n", byteRange: "bytes=0-3"},
				{method: http.MethodHead, body: "new\n", complete: true},
			},
			output: "old attempt\nnew\n",
		},
		{
			name: "empty replacement waits for new bytes",
			responses: []logBlobResponse{
				{method: http.MethodHead, body: "old\n", etag: `"1"`},
				{method: http.MethodGet, body: "old\n", byteRange: "bytes=0-3"},
				{method: http.MethodHead},
				{method: http.MethodHead, body: "new\n", etag: `"2"`},
				{method: http.MethodGet, body: "new\n", byteRange: "bytes=0-3"},
				{method: http.MethodHead, body: "new\n", complete: true},
			},
			output: "old\nnew\n",
		},
		{
			name: "empty completed replacement does not download a negative range",
			responses: []logBlobResponse{
				{method: http.MethodHead, body: "old\n", etag: `"1"`},
				{method: http.MethodGet, body: "old\n", byteRange: "bytes=0-3"},
				{method: http.MethodHead, complete: true},
			},
			output: "old\n",
		},
		{
			name: "range rejected after HEAD resets even if the new log grows before the next HEAD",
			responses: []logBlobResponse{
				{method: http.MethodHead, body: "old\n", etag: `"1"`},
				{method: http.MethodGet, body: "old\n", byteRange: "bytes=0-3"},
				{method: http.MethodHead, body: "old\nmore\n", etag: `"2"`},
				{method: http.MethodGet, byteRange: "bytes=4-8", status: http.StatusRequestedRangeNotSatisfiable},
				{method: http.MethodHead, body: "new attempt\n", etag: `"3"`},
				{method: http.MethodGet, body: "new attempt\n", byteRange: "bytes=0-11"},
				{method: http.MethodHead, body: "new attempt\n", complete: true},
			},
			output: "old\nnew attempt\n",
		},
		{
			name: "conditional read retries an append race without replay",
			responses: []logBlobResponse{
				{method: http.MethodHead, body: "a\n", etag: `"1"`},
				{method: http.MethodGet, body: "a\n", byteRange: "bytes=0-1", ifMatch: `"1"`},
				{method: http.MethodHead, body: "a\nb\n", etag: `"2"`},
				{method: http.MethodGet, byteRange: "bytes=2-3", ifMatch: `"2"`, status: http.StatusPreconditionFailed},
				{method: http.MethodHead, body: "a\nb\nc\n", etag: `"3"`},
				{method: http.MethodGet, body: "b\nc\n", byteRange: "bytes=2-5", ifMatch: `"3"`},
				{method: http.MethodHead, body: "a\nb\nc\n", complete: true},
			},
			output: "a\nb\nc\n",
		},
		{
			name: "deleted log resets before a larger replacement appears",
			responses: []logBlobResponse{
				{method: http.MethodHead, body: "old\n", etag: `"1"`},
				{method: http.MethodGet, body: "old\n", byteRange: "bytes=0-3"},
				{method: http.MethodHead, status: http.StatusNotFound},
				{method: http.MethodHead, body: "new attempt\n", etag: `"2"`},
				{method: http.MethodGet, body: "new attempt\n", byteRange: "bytes=0-11"},
				{method: http.MethodHead, body: "new attempt\n", complete: true},
			},
			output: "old\nnew attempt\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := logBlobClient(t, tt.responses...)
			var output strings.Builder
			require.NoError(t, streamLogs(t.Context(), client, &output))
			require.Equal(t, tt.output, output.String())
		})
	}
}

func TestStreamLogs_CancellationDuringRangeRecovery(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusPreconditionFailed, http.StatusRequestedRangeNotSatisfiable} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			client := logBlobClient(t,
				logBlobResponse{method: http.MethodHead, body: "data\n", etag: `"1"`},
				logBlobResponse{method: http.MethodGet, byteRange: "bytes=0-4", status: status},
				logBlobResponse{method: http.MethodHead, body: "data\n", etag: `"2"`},
				logBlobResponse{method: http.MethodGet, byteRange: "bytes=0-4", status: status, before: cancel},
			)
			require.ErrorIs(t, streamLogs(ctx, client, io.Discard), context.Canceled)
		})
	}
}

func TestStreamLogs_HTTPFailuresRemainErrors(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodHead, http.MethodGet} {
		for _, status := range []int{http.StatusForbidden, http.StatusInternalServerError} {
			t.Run(fmt.Sprintf("%s/%d", method, status), func(t *testing.T) {
				t.Parallel()

				var responses []logBlobResponse
				if method == http.MethodGet {
					responses = append(responses, logBlobResponse{
						method: http.MethodHead, body: "data\n", etag: `"1"`,
					})
				}
				failure := logBlobResponse{method: method, status: status}
				if method == http.MethodGet {
					failure.byteRange = "bytes=0-4"
				}
				responses = append(responses, failure)
				err := streamLogs(t.Context(), logBlobClient(t, responses...), io.Discard)
				var responseErr *azcore.ResponseError
				require.ErrorAs(t, err, &responseErr)
				require.Equal(t, status, responseErr.StatusCode)
			})
		}
	}
}

type failingLogWriter struct {
	err error
}

func (w failingLogWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func TestStreamLogs_WriterFailureIsNotRetried(t *testing.T) {
	t.Parallel()

	expected := errors.New("writer failed")
	client := logBlobClient(t,
		logBlobResponse{method: http.MethodHead, body: "data\n", etag: `"1"`},
		logBlobResponse{method: http.MethodGet, body: "data\n", byteRange: "bytes=0-4"},
	)
	require.ErrorIs(t, streamLogs(t.Context(), client, failingLogWriter{err: expected}), expected)
}
