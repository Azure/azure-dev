// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func TestServiceRequestID(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers http.Header
		body    string
		want    string
	}{
		{"Azure", http.Header{"X-Ms-Request-Id": {"azure-123"}}, "", "azure-123"},
		{"gateway", http.Header{"Apim-Request-Id": {"gateway-123"}}, "", "gateway-123"},
		{"OpenAI", http.Header{"X-Request-Id": {"openai-123"}}, "", "openai-123"},
		{"fine-tuning", http.Header{"Request-Id": {"finetune-123"}}, "", "finetune-123"},
		{"body", nil, `{"correlation":{"request":"body-123"}}`, "body-123"},
		{"header precedence", http.Header{"X-Ms-Request-Id": {"azure-123"}, "X-Request-Id": {"other-123"}},
			`{"correlation":{"request":"body-123"}}`, "azure-123"},
		{"whitespace", http.Header{"Request-Id": {"  request-123  "}}, "", "request-123"},
		{"unsafe header", http.Header{"X-Ms-Request-Id": {"secret\ninjected"}}, "", ""},
		{"credential URL", http.Header{"Request-Id": {"https://user:password@example.com?sig=secret"}}, "", ""},
		{"oversized", http.Header{"Request-Id": {strings.Repeat("a", 129)}}, "", ""},
		{"unsafe body", nil, `{"correlation":{"request":"sig=secret"}}`, ""},
		{"invalid header fallback", http.Header{"X-Ms-Request-Id": {"bad\nvalue"}},
			`{"correlation":{"request":"body-123"}}`, "body-123"},
		{"missing", nil, `{}`, ""},
		{"malformed", nil, `{`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := serviceRequestID(tc.headers, []byte(tc.body)); got != tc.want {
				t.Fatalf("request ID = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestServiceFailuresPreserveRequestIDs(t *testing.T) {
	for _, service := range []string{"RLE", "fine-tuning"} {
		for _, tc := range []struct {
			name   string
			status int
			header string
			body   string
			wantID string
		}{
			{"bad request", 400, "request-400", `{"error":{"code":"InvalidPayload","message":"invalid input"}}`, "request-400"},
			{"unauthorized", 401, "request-401", "", "request-401"},
			{"forbidden", 403, "request-403", "", "request-403"},
			{"not found", 404, "request-404", "", "request-404"},
			{"throttled", 429, "request-429", "retry later", "request-429"},
			{"server error", 500, "request-500", "<html>unavailable</html>", "request-500"},
			{"body correlation", 500, "", `{"correlation":{"request":"body-123"}}`, "body-123"},
			{"missing ID", 500, "", "unavailable", ""},
			{"unsafe header", 500, "https://user:password@example.com?sig=secret", "unavailable", ""},
		} {
			t.Run(service+"/"+tc.name, func(t *testing.T) {
				transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
					headers := http.Header{}
					headers.Set("x-ms-request-id", tc.header)
					return &http.Response{
						StatusCode: tc.status,
						Header:     headers,
						Body:       io.NopCloser(strings.NewReader(tc.body)),
					}, nil
				})
				var err error
				if service == "RLE" {
					client := newRleClientWithCredential("https://rle.example.com", &testTokenCredential{})
					client.httpClient.Transport = transport
					err = client.do(t.Context(), http.MethodGet, environmentCollectionPath, nil, nil)
					if err == nil {
						t.Fatal("expected service failure")
					}
					if isRleNotFound(err) != (tc.status == http.StatusNotFound) {
						t.Fatal("not-found classification changed")
					}
					err = serviceError(fmt.Errorf("command failed: %w", err))
				} else {
					client := newFinetuneClientWithCredential("https://resource.openai.azure.com", &testTokenCredential{})
					client.httpClient.Transport = transport
					_, err = client.createJob(t.Context(), finetuneJobCreationRequest{}, "project")
					if err == nil {
						t.Fatal("expected submission failure")
					}
					err = finetuneServiceError(fmt.Errorf("command failed: %w", err))
				}
				serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
				if !ok || serviceErr.StatusCode != tc.status {
					t.Fatalf("service classification lost: %v", err)
				}
				message := azdext.WrapError(err).GetMessage()
				if tc.wantID != "" {
					if !strings.Contains(message, "Request ID: "+tc.wantID) {
						t.Fatalf("request ID lost during serialization: %q", message)
					}
				} else if strings.Contains(message, "Request ID:") {
					t.Fatalf("unexpected request ID: %q", message)
				}
				for _, secret := range []string{"password", "sig=secret"} {
					if strings.Contains(message, secret) {
						t.Fatalf("unsafe request ID leaked into message: %q", message)
					}
				}
			})
		}
	}
}

func TestTrainSubmissionFailureShowsRequestID(t *testing.T) {
	action, output := stubbedTrain(t, t.Context(), &rleTrainFlags{noFollow: true})
	client, err := createFinetuneClient("https://account.openai.azure.com")
	if err != nil {
		t.Fatal(err)
	}
	originalTransport := client.httpClient.Transport
	client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != finetuneJobsPath {
			return originalTransport.RoundTrip(request)
		}
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Header:     http.Header{"Request-Id": {"submit-123"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"submission failed"}}`)),
		}, nil
	})
	err = action.Run()
	if err == nil || !strings.Contains(azdext.WrapError(err).GetMessage(), "Request ID: submit-123") {
		t.Fatalf("train submission request ID missing: %v", err)
	}
	if strings.Contains(output.String(), "Submitted fine-tuning job") {
		t.Fatalf("failed submission reported success: %q", output.String())
	}
}

func TestResponseFailuresPreserveRequestIDAndCause(t *testing.T) {
	for _, service := range []string{"RLE", "fine-tuning"} {
		for _, failure := range []string{"read", "decode"} {
			t.Run(service+"/"+failure, func(t *testing.T) {
				var body io.ReadCloser = io.NopCloser(strings.NewReader("{"))
				if failure == "read" {
					body = io.NopCloser(iotest.ErrReader(io.ErrUnexpectedEOF))
				}
				transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Request-Id": {"response-123"}},
						Body:       body,
					}, nil
				})
				var target any
				var err error
				if service == "RLE" {
					client := newRleClientWithCredential("https://rle.example.com", &testTokenCredential{})
					client.httpClient.Transport = transport
					err = client.do(t.Context(), http.MethodGet, environmentCollectionPath, nil, &target)
				} else {
					client := newFinetuneClientWithCredential("https://resource.openai.azure.com", &testTokenCredential{})
					client.httpClient.Transport = transport
					err = client.do(t.Context(), http.MethodGet, finetuneJobsPath, nil, nil, &target)
				}
				if err == nil || !strings.HasSuffix(err.Error(), "\nRequest ID: response-123") {
					t.Fatalf("response request ID lost: %v", err)
				}
				if failure == "read" && !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatalf("response read cause lost: %v", err)
				}
			})
		}
	}
}

func TestRolloutWebSocketFailuresShowRequestID(t *testing.T) {
	response := &http.Response{
		StatusCode: http.StatusForbidden,
		Header:     http.Header{"X-Ms-Request-Id": {"upgrade-123"}},
		Body:       io.NopCloser(strings.NewReader("access denied")),
	}
	cause := errors.New("handshake failed")
	err := newExecuteRolloutHandshakeError(cause, response)
	if !strings.Contains(serviceError(err).Error(), "Request ID: upgrade-123") || !errors.Is(err, cause) {
		t.Fatalf("handshake diagnostics lost: %v", err)
	}
	frameErr := newExecuteRolloutFrameError([]byte(
		`{"code":"EnvironmentNotFound","message":"not found","correlation":{"request":"frame-123"}}`,
	))
	if !isRleNotFound(frameErr) || !strings.Contains(serviceError(frameErr).Error(), "Request ID: frame-123") {
		t.Fatalf("frame diagnostics lost: %v", frameErr)
	}
}
