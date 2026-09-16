// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func TestRequestBodyWrapsAction(t *testing.T) {
	body, err := requestBody("step", &callOptions{action: `{"message":"hello"}`})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"action":{"message":"hello"}}` {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestRequestBodyUsesRawBody(t *testing.T) {
	body, err := requestBody("step", &callOptions{
		action: `{"message":"hello"}`,
		body:   `{"action":{"message":"override"},"metadata":{"x":1}}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	expected := `{"action":{"message":"override"},"metadata":{"x":1}}`
	if string(body) != expected {
		t.Fatalf("expected %s, got %s", expected, body)
	}
}

func TestNormalizeOperation(t *testing.T) {
	operation, err := normalizeOperation("/STEP")
	if err != nil {
		t.Fatal(err)
	}
	if operation != "step" {
		t.Fatalf("expected step, got %q", operation)
	}
	if _, err := normalizeOperation("unknown"); err == nil {
		t.Fatal("expected unknown operation to fail")
	}
}

func TestRunShellCallsCommands(t *testing.T) {
	var stepBody map[string]any
	var resetBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/reset":
			if err := json.NewDecoder(r.Body).Decode(&resetBody); err != nil {
				t.Errorf("decode reset body: %v", err)
			}
			_, _ = w.Write([]byte(`{"reset":true}`))
		case "/step":
			if err := json.NewDecoder(r.Body).Decode(&stepBody); err != nil {
				t.Errorf("decode step body: %v", err)
			}
			_, _ = w.Write([]byte(`{"reward":1}`))
		case "/state":
			_, _ = w.Write([]byte(`{"state":"ready"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	input := strings.NewReader("health\nreset {\"seed\":0}\nstep {\"message\":\"hello\"}\nstate\nexit\n")
	var output bytes.Buffer
	if err := runShell(t.Context(), input, &output, server.URL, 30, nil); err != nil {
		t.Fatal(err)
	}

	if resetBody["seed"] != float64(0) {
		t.Fatalf("expected reset seed body, got %#v", resetBody)
	}
	action, ok := stepBody["action"].(map[string]any)
	if !ok || action["message"] != "hello" {
		t.Fatalf("expected wrapped step action, got %#v", stepBody)
	}
	if !strings.Contains(output.String(), `"reward": 1`) {
		t.Fatalf("expected pretty step response in output, got %s", output.String())
	}
}

func TestRunShellRequiresStepPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/step" {
			t.Fatal("step without payload should not call the runtime")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	input := strings.NewReader("step\nexit\n")
	var output bytes.Buffer
	if err := runShell(t.Context(), input, &output, server.URL, 30, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "step requires a JSON action payload") {
		t.Fatalf("expected step payload error, got %s", output.String())
	}
}

func TestRunShellRefreshesAuthorizationForEachRequest(t *testing.T) {
	var authorizations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		if got := r.URL.Query().Get("api-version"); got != "test-version" {
			t.Errorf("expected preserved API version, got %q", got)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	tokenNumber := 0
	authorizationProvider := func(context.Context) (string, error) {
		tokenNumber++
		return fmt.Sprintf("Bearer token-%d", tokenNumber), nil
	}
	input := strings.NewReader("health\nstate\nexit\n")
	var output bytes.Buffer
	if err := runShell(
		t.Context(),
		input,
		&output,
		server.URL+"?api-version=test-version",
		30,
		authorizationProvider,
	); err != nil {
		t.Fatal(err)
	}

	expected := []string{"Bearer token-1", "Bearer token-2"}
	if !slices.Equal(authorizations, expected) {
		t.Fatalf("expected refreshed authorization headers %v, got %v", expected, authorizations)
	}
}

func TestHTTPClientDoesNotFollowRedirects(t *testing.T) {
	redirectedRequests := 0
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedRequests++
		http.Error(w, "unexpected redirected request", http.StatusInternalServerError)
	}))
	defer redirectTarget.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL, http.StatusFound)
	}))
	defer server.Close()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err := HTTPClient(30).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusFound {
		t.Fatalf("expected redirect response, got HTTP %d", response.StatusCode)
	}
	if redirectedRequests != 0 {
		t.Fatalf("expected no redirected requests, got %d", redirectedRequests)
	}
}

func TestWaitForHealthDoesNotFollowRedirects(t *testing.T) {
	redirectedRequests := 0
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedRequests++
		http.Error(w, "unexpected redirected health request", http.StatusInternalServerError)
	}))
	defer redirectTarget.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL, http.StatusFound)
	}))
	defer server.Close()

	err := WaitForHealthWithAuthorizationProvider(
		t.Context(),
		server.URL,
		10*time.Millisecond,
		func(context.Context) (string, error) {
			return "Bearer secret", nil
		},
	)
	if err == nil {
		t.Fatal("expected redirected health response to fail")
	}
	if redirectedRequests != 0 {
		t.Fatalf("expected no redirected health requests, got %d", redirectedRequests)
	}
}

func TestWaitForHealthReportsRemoteResponseDetail(t *testing.T) {
	responseBody := `{"error":{"code":"BadRequest","message":"Missing required query parameter: api-version"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, responseBody, http.StatusBadRequest)
	}))
	defer server.Close()

	err := WaitForHealthWithAuthorizationProvider(t.Context(), server.URL, 10*time.Millisecond, nil)
	if err == nil {
		t.Fatal("expected health failure")
	}
	if !strings.Contains(err.Error(), responseBody) {
		t.Fatalf("expected response detail in health error, got %v", err)
	}
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T", err)
	}
	if localErr.Code != "rle_remote_runtime_not_ready" {
		t.Fatalf("expected remote runtime error code, got %q", localErr.Code)
	}
	if localErr.Suggestion != "Check the remote RLE instance status and OpenEnv service logs, then retry." {
		t.Fatalf("unexpected remote health suggestion: %q", localErr.Suggestion)
	}
}

func TestWaitForHealthReportsLocalContainerSuggestion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	err := WaitForHealth(server.URL, 10*time.Millisecond)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T", err)
	}
	if localErr.Code != "rle_local_container_not_ready" {
		t.Fatalf("expected local container error code, got %q", localErr.Code)
	}
	if localErr.Suggestion != "Check the local container logs, then retry." {
		t.Fatalf("unexpected local health suggestion: %q", localErr.Suggestion)
	}
}

func TestRuntimeOperationURLPreservesQuery(t *testing.T) {
	runtimeUrl, err := RuntimeOperationURL("https://example.test/openenv?api-version=test-version", "health")
	if err != nil {
		t.Fatal(err)
	}
	if runtimeUrl != "https://example.test/openenv/health?api-version=test-version" {
		t.Fatalf("unexpected runtime URL: %s", runtimeUrl)
	}
}

func TestRunShellWithContextReturnsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	cancel()

	var output bytes.Buffer
	if err := RunShellWithContext(ctx, reader, &output, "http://127.0.0.1", 30); err != nil {
		t.Fatal(err)
	}
}
