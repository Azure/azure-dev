// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	if err := runShell(input, &output, server.URL, 30); err != nil {
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
	if err := runShell(input, &output, server.URL, 30); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "step requires a JSON action payload") {
		t.Fatalf("expected step payload error, got %s", output.String())
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
