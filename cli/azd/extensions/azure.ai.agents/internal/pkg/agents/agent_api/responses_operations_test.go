// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

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
