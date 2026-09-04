// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type trackedResponseBody struct {
	io.Reader
	closed bool
}

func (b *trackedResponseBody) Close() error {
	b.closed = true
	return nil
}

type trackedResponseTransport struct {
	body *trackedResponseBody
}

func (t *trackedResponseTransport) Do(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       t.body,
		Request:    req,
	}, nil
}

func TestCreateResponseUsesProjectEndpoint(t *testing.T) {
	client, transport := newCaptureClient(http.StatusOK, `{"id":"resp_1"}`)
	body, _, err := client.CreateResponse(
		t.Context(),
		[]byte(`{"input":"hello","agent_reference":{"type":"agent_reference","name":"agent"}}`),
		map[string]string{"x-model-endpoint": "https://model.example.com"},
	)

	if err != nil {
		t.Fatalf("CreateResponse: %v", err)
	}
	if len(transport.requests) != 1 {
		t.Fatal("expected request")
	}
	request := transport.requests[0]
	requestBody, _ := io.ReadAll(request.Body)
	if request.URL.Path != "/api/projects/proj/openai/v1/responses" {
		t.Errorf("path: got %q", request.URL.Path)
	}
	if request.URL.RawQuery != "" {
		t.Errorf("query: got %q", request.URL.RawQuery)
	}
	if request.Header.Get("x-model-endpoint") != "https://model.example.com" {
		t.Errorf("x-model-endpoint: got %q", request.Header.Get("x-model-endpoint"))
	}
	if !strings.Contains(string(requestBody), `"agent_reference"`) {
		t.Errorf("body: got %s", requestBody)
	}
	if !strings.Contains(string(body), "resp_1") {
		t.Errorf("response body: got %s", body)
	}
}

func TestResponseLifecycleUsesProjectEndpoint(t *testing.T) {
	client, transport := newCaptureClient(http.StatusOK, `{"id":"resp_1"}`)
	if _, _, err := client.GetResponse(t.Context(), "resp/1"); err != nil {
		t.Fatalf("GetResponse: %v", err)
	}
	if _, _, err := client.CancelResponse(t.Context(), "resp/1"); err != nil {
		t.Fatalf("CancelResponse: %v", err)
	}
	if err := client.DeleteResponse(t.Context(), "resp/1"); err != nil {
		t.Fatalf("DeleteResponse: %v", err)
	}

	paths := make([]string, len(transport.requests))
	for i, request := range transport.requests {
		paths[i] = request.Method + " " + request.URL.EscapedPath()
	}
	want := []string{
		"GET /api/projects/proj/openai/v1/responses/resp%2F1",
		"POST /api/projects/proj/openai/v1/responses/resp%2F1/cancel",
		"DELETE /api/projects/proj/openai/v1/responses/resp%2F1",
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("request %d: got %q, want %q", i, paths[i], want[i])
		}
	}
}
func TestCreateResponseStreamAtUsesExplicitEndpoint(t *testing.T) {
	client, transport := newCaptureClient(http.StatusOK, "event: response.completed\n\n")
	endpoint := "https://test.example.com/api/projects/proj/agents/managed/endpoint/protocols/openai/responses?api-version=v1"

	stream, _, err := client.CreateResponseStreamAt(
		t.Context(), endpoint, []byte(`{"input":"hello"}`),
		map[string]string{"Foundry-Features": "GitHubCopilot=V1Preview"})
	if err != nil {
		t.Fatalf("CreateResponseStreamAt: %v", err)
	}
	defer stream.Close()

	if len(transport.requests) != 1 {
		t.Fatalf("requests: got %d", len(transport.requests))
	}
	request := transport.requests[0]
	if request.URL.String() != endpoint {
		t.Errorf("endpoint: got %q, want %q", request.URL.String(), endpoint)
	}
	if request.Header.Get("Foundry-Features") != "GitHubCopilot=V1Preview" {
		t.Errorf("feature header: got %q", request.Header.Get("Foundry-Features"))
	}
}

func TestCreateResponseStreamAtClosesFailedResponse(t *testing.T) {
	body := &trackedResponseBody{Reader: strings.NewReader(`{"error":{"message":"invalid request"}}`)}
	client := newTestClient(
		"https://test.example.com/api/projects/proj",
		&trackedResponseTransport{body: body},
	)

	stream, _, err := client.CreateResponseStreamAt(
		t.Context(),
		"https://test.example.com/api/projects/proj/openai/v1/responses",
		[]byte(`{"input":"hello"}`),
		nil,
	)
	if err == nil {
		t.Fatal("expected a response error")
	}
	if stream != nil {
		t.Fatal("failed response must not return a stream")
	}
	if !body.closed {
		t.Fatal("failed response body was not closed")
	}
}
