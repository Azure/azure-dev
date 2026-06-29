// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenEnvRequestBodyWrapsAction(t *testing.T) {
	body, err := openEnvRequestBody("step", &openEnvInvokeFlags{action: `{"message":"hello"}`})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"action":{"message":"hello"}}` {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestOpenEnvRequestBodyUsesRawBody(t *testing.T) {
	body, err := openEnvRequestBody("step", &openEnvInvokeFlags{
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

func TestNormalizeOpenEnvOperation(t *testing.T) {
	operation, err := normalizeOpenEnvOperation("/STEP")
	if err != nil {
		t.Fatal(err)
	}
	if operation != "step" {
		t.Fatalf("expected step, got %q", operation)
	}
	if _, err := normalizeOpenEnvOperation("unknown"); err == nil {
		t.Fatal("expected unknown operation to fail")
	}
}

func TestRunOpenEnvShellCallsCommands(t *testing.T) {
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
	if err := runOpenEnvShell(input, &output, server.URL, 30); err != nil {
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

func TestRunOpenEnvShellRequiresStepPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/step" {
			t.Fatal("step without payload should not call the runtime")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	input := strings.NewReader("step\nexit\n")
	var output bytes.Buffer
	if err := runOpenEnvShell(input, &output, server.URL, 30); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "step requires a JSON action payload") {
		t.Fatalf("expected step payload error, got %s", output.String())
	}
}

func TestLocalContainerNamesUseEnvironmentName(t *testing.T) {
	if name := localContainerName("code_rl"); name != "azd-rle-code-rl" {
		t.Fatalf("expected local container name, got %q", name)
	}
}

func TestDockerPullFailureSuggestionUsesImageName(t *testing.T) {
	suggestion := dockerPullFailureSuggestion("devrle.azurecr.io/echo-rl:latest")
	if !strings.Contains(suggestion, "Ensure Docker can pull devrle.azurecr.io/echo-rl:latest") {
		t.Fatalf("expected Docker pull guidance, got %q", suggestion)
	}
	if strings.Contains(suggestion, "az ") {
		t.Fatalf("did not expect Azure sign-in guidance, got %q", suggestion)
	}
}

func TestLocalRuntimeImageTreatsManifestAsAuthoritative(t *testing.T) {
	tempDir := t.TempDir()
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalDir); err != nil {
			t.Fatal(err)
		}
	})

	manifest := []byte(`
name: code_rl
template:
  kind: openenv
  local:
    image:
  environment:
    image:
`)
	if err := os.WriteFile(filepath.Join(tempDir, rleManifestFile), manifest, 0600); err != nil {
		t.Fatal(err)
	}

	_, err = localRuntimeImage(rleState{
		Name:       "code_rl",
		LocalImage: "example.azurecr.io/stale-local:latest",
		Image:      "example.azurecr.io/stale-env:latest",
	})
	if err == nil {
		t.Fatal("expected empty manifest images to require an image")
	}
}
