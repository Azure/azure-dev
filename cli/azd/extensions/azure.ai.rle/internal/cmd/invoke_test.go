// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func TestOpenEnvRequestBodyWrapsAction(t *testing.T) {
	body, err := openEnvRequestBody("step", &openEnvCallFlags{action: `{"message":"hello"}`})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"action":{"message":"hello"}}` {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestOpenEnvRequestBodyUsesRawBody(t *testing.T) {
	body, err := openEnvRequestBody("step", &openEnvCallFlags{
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

func TestInvokeRemoteCreatesSandboxAndRunsShell(t *testing.T) {
	captureBrowserOpen(t)
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	if err := saveRleState(rleState{
		Name:               "code_rl",
		ProjectEndpoint:    "https://account.services.ai.azure.com/api/projects/project-1",
		EnvironmentId:      "env-1",
		EnvironmentVersion: "v1",
	}); err != nil {
		t.Fatal(err)
	}

	envServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"status":"healthy"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer envServer.Close()

	var sandboxBody map[string]any
	deleteCalled := false
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost &&
			r.URL.Path == "/rle/v1.0/projects/project-1/environments/env-1/sandboxes":
			if err := json.NewDecoder(r.Body).Decode(&sandboxBody); err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte(`{"id":"sandbox-1","status":"Running","url":` + strconv.Quote(envServer.URL) + `}`))
		case r.Method == http.MethodDelete &&
			r.URL.Path == "/rle/v1.0/projects/project-1/environments/env-1/sandboxes/sandbox-1":
			deleteCalled = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected sandbox request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer controlPlane.Close()
	t.Setenv("RLE_ENDPOINT", controlPlane.URL)

	command := newInvokeCommand()
	command.SetIn(strings.NewReader("health\nexit\n"))
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if sandboxBody["version"] != "v1" {
		t.Fatalf("expected sandbox version, got %#v", sandboxBody)
	}
	if !strings.Contains(output.String(), "Sandbox sandbox-1 ready at "+envServer.URL) {
		t.Fatalf("expected sandbox ready output, got %s", output.String())
	}
	if !strings.Contains(output.String(), `"status": "healthy"`) {
		t.Fatalf("expected remote shell health output, got %s", output.String())
	}
	if !deleteCalled {
		t.Fatal("expected remote invoke to release the sandbox")
	}
}

func TestInvokeRemoteUsesSandboxWebWhenAvailable(t *testing.T) {
	openedUrl := captureBrowserOpen(t)
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	if err := saveRleState(rleState{
		Name:            "code_rl",
		ProjectEndpoint: "https://account.services.ai.azure.com/api/projects/project-1",
		EnvironmentId:   "env-1",
	}); err != nil {
		t.Fatal(err)
	}

	envServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"status":"healthy"}`))
		case "/web":
			_, _ = w.Write([]byte(`<html>sandbox ui</html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer envServer.Close()

	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost &&
			r.URL.Path == "/rle/v1.0/projects/project-1/environments/env-1/sandboxes":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sandbox-1","status":"Running","url":` + strconv.Quote(envServer.URL) + `}`))
		case r.Method == http.MethodDelete &&
			r.URL.Path == "/rle/v1.0/projects/project-1/environments/env-1/sandboxes/sandbox-1":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected sandbox request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer controlPlane.Close()
	t.Setenv("RLE_ENDPOINT", controlPlane.URL)

	command := newInvokeCommand()
	command.SetIn(strings.NewReader("exit\n"))
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Playground UI: "+envServer.URL+"/web") {
		t.Fatalf("expected sandbox web URL, got %s", output.String())
	}
	if *openedUrl != envServer.URL+"/web" {
		t.Fatalf("expected browser to open sandbox web URL, got %q", *openedUrl)
	}
}

func TestRemotePlaygroundProxyForwardsToSandbox(t *testing.T) {
	envServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/web":
			http.NotFound(w, r)
		case "/state":
			_, _ = w.Write([]byte(`{"step_count":3}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer envServer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	playgroundUrl, stop, err := remotePlaygroundUrl(ctx, envServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if !strings.Contains(playgroundUrl, "127.0.0.1") || !strings.HasSuffix(playgroundUrl, "/web") {
		t.Fatalf("expected local playground URL, got %q", playgroundUrl)
	}

	stateUrl := strings.TrimSuffix(playgroundUrl, "/web") + "/state"
	resp, err := http.Get(stateUrl) //nolint:gosec // Test-only local proxy URL.
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"step_count":3}` {
		t.Fatalf("expected proxied state body, got %s", body)
	}
}

func TestInvokeRemotePollsSandboxUntilRunning(t *testing.T) {
	captureBrowserOpen(t)
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	if err := saveRleState(rleState{
		Name:            "code_rl",
		ProjectEndpoint: "https://account.services.ai.azure.com/api/projects/project-1",
		EnvironmentId:   "env-1",
	}); err != nil {
		t.Fatal(err)
	}

	oldPollInterval := remoteSandboxPollInterval
	remoteSandboxPollInterval = time.Millisecond
	defer func() { remoteSandboxPollInterval = oldPollInterval }()

	envServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/health" {
			_, _ = w.Write([]byte(`{"status":"healthy"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer envServer.Close()

	getCount := 0
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost &&
			r.URL.Path == "/rle/v1.0/projects/project-1/environments/env-1/sandboxes":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sandbox-1","status":"Starting"}`))
		case r.Method == http.MethodGet &&
			r.URL.Path == "/rle/v1.0/projects/project-1/environments/env-1/sandboxes/sandbox-1":
			getCount++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sandbox-1","status":"Running","url":` + strconv.Quote(envServer.URL) + `}`))
		case r.Method == http.MethodDelete &&
			r.URL.Path == "/rle/v1.0/projects/project-1/environments/env-1/sandboxes/sandbox-1":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected sandbox request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer controlPlane.Close()
	t.Setenv("RLE_ENDPOINT", controlPlane.URL)

	command := newInvokeCommand()
	command.SetIn(strings.NewReader("exit\n"))
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if getCount != 1 {
		t.Fatalf("expected one sandbox poll, got %d", getCount)
	}
	if !strings.Contains(output.String(), "Sandbox sandbox-1 ready at "+envServer.URL) {
		t.Fatalf("expected sandbox ready output, got %s", output.String())
	}
}

func TestInvokeRemoteFailsWhenSandboxFails(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	if err := saveRleState(rleState{
		Name:            "code_rl",
		ProjectEndpoint: "https://account.services.ai.azure.com/api/projects/project-1",
		EnvironmentId:   "env-1",
	}); err != nil {
		t.Fatal(err)
	}

	deleteCalled := false
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost &&
			r.URL.Path == "/rle/v1.0/projects/project-1/environments/env-1/sandboxes":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sandbox-1","status":"Failed","error":"image pull failed"}`))
		case r.Method == http.MethodDelete &&
			r.URL.Path == "/rle/v1.0/projects/project-1/environments/env-1/sandboxes/sandbox-1":
			deleteCalled = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected sandbox request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer controlPlane.Close()
	t.Setenv("RLE_ENDPOINT", controlPlane.URL)

	command := newInvokeCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	err := command.Execute()
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T: %v", err, err)
	}
	if localErr.Code != "rle_sandbox_start_failed" {
		t.Fatalf("expected sandbox failed code, got %q", localErr.Code)
	}
	if !deleteCalled {
		t.Fatal("expected failed sandbox to be released")
	}
}

func TestRequireDeployedEnvironmentRejectsMissingEnvironmentId(t *testing.T) {
	err := requireDeployedEnvironment(rleState{ProjectEndpoint: "https://account.services.ai.azure.com/api/projects/project-1"})
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T", err)
	}
	if localErr.Code != "rle_environment_not_deployed" {
		t.Fatalf("expected not deployed code, got %q", localErr.Code)
	}
}

func TestLocalContainerNamesUseEnvironmentName(t *testing.T) {
	if name := localContainerName("code_rl"); name != "azd-rle-code-rl" {
		t.Fatalf("expected local container name, got %q", name)
	}
}

func TestEnsurePortAvailableRejectsBoundPort(t *testing.T) {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	if err := ensurePortAvailable(port); err == nil {
		t.Fatal("expected bound port to fail")
	} else {
		localErr, ok := errors.AsType[*azdext.LocalError](err)
		if !ok {
			t.Fatalf("expected LocalError, got %T", err)
		}
		for _, expected := range []string{
			"docker ps --filter \"publish=",
			"docker rm -f <container>",
			"azd ai rle run --port",
			"netstat -ano | findstr",
		} {
			if !strings.Contains(localErr.Suggestion, expected) {
				t.Fatalf("expected suggestion to contain %q, got %q", expected, localErr.Suggestion)
			}
		}
	}
}

func TestResolvePortDefaultsTo8000WithoutPersistedState(t *testing.T) {
	if port := resolvePort(&localRunFlags{}); port != defaultPort {
		t.Fatalf("expected default port %d, got %d", defaultPort, port)
	}
	if port := resolvePort(&localRunFlags{port: 9000}); port != 9000 {
		t.Fatalf("expected explicit port 9000, got %d", port)
	}
}

func TestLoadLocalRunStateDefaultsToExistingFolderWithoutInit(t *testing.T) {
	tempDir := filepath.Join(t.TempDir(), "My Env")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(tempDir)

	var output bytes.Buffer
	state, err := loadLocalRunState(&localRunFlags{source: "."}, &output)
	if err != nil {
		t.Fatal(err)
	}
	if state.Name != "my-env" {
		t.Fatalf("expected source-folder name, got %q", state.Name)
	}
	image := localRuntimeImageForRun(&localRunFlags{source: "."}, state)
	if image != "my-env:local" {
		t.Fatalf("expected default local image, got %q", image)
	}
	if !strings.Contains(output.String(), "No .azd-rle.json found; using current folder as the RLE source.") {
		t.Fatalf("expected missing state transparency message, got %q", output.String())
	}
	var saved rleState
	data, err := os.ReadFile(stateFilePath("."))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved != (rleState{Name: "my-env"}) {
		t.Fatalf("expected saved state with only name, got %#v", saved)
	}
}

func TestInvokeRemoteWaitsForDiskImageConversion(t *testing.T) {
	captureBrowserOpen(t)
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	if err := saveRleState(rleState{
		Name:            "code_rl",
		ProjectEndpoint: "https://account.services.ai.azure.com/api/projects/project-1",
		EnvironmentId:   "env-1",
	}); err != nil {
		t.Fatal(err)
	}

	oldPollInterval := remoteImagePollInterval
	remoteImagePollInterval = time.Millisecond
	defer func() { remoteImagePollInterval = oldPollInterval }()

	envServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/health" {
			_, _ = w.Write([]byte(`{"status":"healthy"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer envServer.Close()

	createCount := 0
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost &&
			r.URL.Path == "/rle/v1.0/projects/project-1/environments/env-1/sandboxes":
			createCount++
			if createCount < 3 {
				http.Error(w, "disk conversion status: Pending", http.StatusConflict)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sandbox-1","status":"Running","url":` + strconv.Quote(envServer.URL) + `}`))
		case r.Method == http.MethodDelete &&
			r.URL.Path == "/rle/v1.0/projects/project-1/environments/env-1/sandboxes/sandbox-1":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected sandbox request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer controlPlane.Close()
	t.Setenv("RLE_ENDPOINT", controlPlane.URL)

	command := newInvokeCommand()
	command.SetIn(strings.NewReader("exit\n"))
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if createCount != 3 {
		t.Fatalf("expected sandbox create to retry twice, got %d calls", createCount)
	}
	if !strings.Contains(output.String(), "Getting sandbox ready for testing (status: Pending); waiting") {
		t.Fatalf("expected sandbox readiness wait message, got %s", output.String())
	}
	if !strings.Contains(output.String(), "Sandbox sandbox-1 ready at "+envServer.URL) {
		t.Fatalf("expected sandbox ready output, got %s", output.String())
	}
}

func TestRemoteInvokeStopsRetryingSandboxLeaseConflicts(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	if err := saveRleState(rleState{
		Name:            "code_rl",
		ProjectEndpoint: "https://account.services.ai.azure.com/api/projects/project-1",
		EnvironmentId:   "env-1",
	}); err != nil {
		t.Fatal(err)
	}

	oldPollInterval := remoteImagePollInterval
	remoteImagePollInterval = time.Millisecond
	defer func() { remoteImagePollInterval = oldPollInterval }()

	createCount := 0
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost &&
			r.URL.Path == "/rle/v1.0/projects/project-1/environments/env-1/sandboxes" {
			createCount++
			http.Error(w, `{"error":"quota unavailable"}`, http.StatusConflict)
			return
		}
		t.Fatalf("unexpected sandbox request: %s %s", r.Method, r.URL.Path)
	}))
	defer controlPlane.Close()
	t.Setenv("RLE_ENDPOINT", controlPlane.URL)

	command := newInvokeCommand()
	command.SetIn(strings.NewReader("exit\n"))
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	err := command.Execute()
	if err == nil {
		t.Fatal("expected sandbox lease retry error")
	}
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T", err)
	}
	if localErr.Code != "rle_sandbox_lease_pending_timeout" {
		t.Fatalf("expected sandbox lease timeout code, got %q", localErr.Code)
	}
	if createCount != remoteSandboxLeaseMaxRetries+1 {
		t.Fatalf("expected initial attempt plus max retries, got %d calls", createCount)
	}
	if !strings.Contains(localErr.Message, "Sandbox was not ready for testing") {
		t.Fatalf("expected generic sandbox readiness message, got %q", localErr.Message)
	}
}

func TestSandboxLeasePendingStatusTreatsAnyConflictAsPending(t *testing.T) {
	status, ok := sandboxLeasePendingStatus(&rleHTTPError{
		statusCode: http.StatusConflict,
		body:       `{"error":"different conflict"}`,
	})
	if !ok {
		t.Fatal("expected conflict to be treated as pending")
	}
	if !strings.Contains(status, "different conflict") {
		t.Fatalf("expected conflict body in status, got %q", status)
	}
}

func TestRunOpenEnvShellWithContextReturnsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	cancel()

	var output bytes.Buffer
	if err := runOpenEnvShellWithContext(ctx, reader, &output, "http://127.0.0.1", 30); err != nil {
		t.Fatal(err)
	}
}

func captureBrowserOpen(t *testing.T) *string {
	t.Helper()
	old := openBrowserFunc
	openedUrl := ""
	openBrowserFunc = func(url string) error {
		openedUrl = url
		return nil
	}
	t.Cleanup(func() {
		openBrowserFunc = old
	})
	return &openedUrl
}

func TestResolveDeployStateDefaultsToExistingFolderWithoutInit(t *testing.T) {
	tempDir := filepath.Join(t.TempDir(), "My Env")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(tempDir)
	t.Setenv(foundryProjectEndpointEnvVar, "https://account.services.ai.azure.com/api/projects/project-1")

	state, initialized, err := resolveDeployState(&rleDeployFlags{})
	if err != nil {
		t.Fatal(err)
	}
	if initialized {
		t.Fatal("expected no saved state")
	}
	if state.Name != "my-env" {
		t.Fatalf("expected source-folder name, got %q", state.Name)
	}
	if state.ProjectEndpoint != "https://account.services.ai.azure.com/api/projects/project-1" {
		t.Fatalf("expected saved project endpoint, got %q", state.ProjectEndpoint)
	}
}

func TestResolveDeployStateDoesNotPersistDockerfileFlag(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(foundryProjectEndpointEnvVar, "https://account.services.ai.azure.com/api/projects/project-1")

	state, initialized, err := resolveDeployState(&rleDeployFlags{
		dockerfile: "server/Dockerfile",
	})
	if err != nil {
		t.Fatal(err)
	}
	if initialized {
		t.Fatal("expected no saved state")
	}
	if state.Name != filepath.Base(tempDir) {
		t.Fatalf("expected source folder name, got %q", state.Name)
	}
}

func TestResolveDeployImageUsesTerminalAcrRegistryEnvironment(t *testing.T) {
	t.Setenv("AZURE_CONTAINER_REGISTRY_ENDPOINT", "example.azurecr.io")

	image, err := resolveDeployImage(
		&rleDeployFlags{},
		rleState{Name: "My Env", ProjectEndpoint: "https://account.services.ai.azure.com/api/projects/Project 1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if image != "example.azurecr.io/project-1-my-env:latest" {
		t.Fatalf("expected derived ACR image, got %q", image)
	}
}

func TestResolveDeployImageRequiresAcrRegistry(t *testing.T) {
	_, err := resolveDeployImage(
		&rleDeployFlags{},
		rleState{Name: "my-env", ProjectEndpoint: "https://account.services.ai.azure.com/api/projects/project-1"},
	)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T", err)
	}
	if localErr.Code != "rle_acr_registry_required" {
		t.Fatalf("expected registry required code, got %q", localErr.Code)
	}
}

func TestResolveDeployImageUsesRegistryEvenWhenStateExists(t *testing.T) {
	t.Setenv("AZURE_CONTAINER_REGISTRY_ENDPOINT", "example.azurecr.io")

	image, err := resolveDeployImage(
		&rleDeployFlags{},
		rleState{
			Name:            "my-env",
			ProjectEndpoint: "https://account.services.ai.azure.com/api/projects/project-1",
			EnvironmentId:   "env-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if image != "example.azurecr.io/project-1-my-env:latest" {
		t.Fatalf("expected registry-derived image, got %q", image)
	}
}

func TestResolveDockerBuildFindsRootDockerfile(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "Dockerfile"), []byte("FROM scratch\n"), 0600); err != nil {
		t.Fatal(err)
	}

	source, dockerfile, cleanup, err := prepareDockerBuild(dockerBuildOptions{source: tempDir})
	if err != nil {
		t.Fatal(err)
	}
	if cleanup != nil {
		t.Fatal("expected no cleanup for existing source")
	}
	if source != tempDir {
		t.Fatalf("expected source %q, got %q", tempDir, source)
	}
	if dockerfile != filepath.Join(tempDir, "Dockerfile") {
		t.Fatalf("expected root Dockerfile, got %q", dockerfile)
	}
}

func TestResolveDockerBuildFindsServerDockerfile(t *testing.T) {
	tempDir := t.TempDir()
	serverDir := filepath.Join(tempDir, "server")
	if err := os.MkdirAll(serverDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(serverDir, "Dockerfile"), []byte("FROM scratch\n"), 0600); err != nil {
		t.Fatal(err)
	}

	source, dockerfile, cleanup, err := prepareDockerBuild(dockerBuildOptions{source: tempDir})
	if err != nil {
		t.Fatal(err)
	}
	if cleanup != nil {
		t.Fatal("expected no cleanup for existing source")
	}
	if source != tempDir {
		t.Fatalf("expected source %q, got %q", tempDir, source)
	}
	if dockerfile != filepath.Join(serverDir, "Dockerfile") {
		t.Fatalf("expected server Dockerfile, got %q", dockerfile)
	}
}

func TestResolveDockerBuildUsesDockerfileOption(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	dockerDir := filepath.Join(tempDir, "docker")
	if err := os.MkdirAll(dockerDir, 0755); err != nil {
		t.Fatal(err)
	}
	customPath := filepath.Join(dockerDir, "custom.Dockerfile")
	if err := os.WriteFile(customPath, []byte("FROM scratch\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, dockerfile, cleanup, err := prepareDockerBuild(dockerBuildOptions{
		source:     tempDir,
		dockerfile: "docker/custom.Dockerfile",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cleanup != nil {
		t.Fatal("expected no cleanup for existing source")
	}
	if dockerfile != customPath {
		t.Fatalf("expected explicit Dockerfile, got %q", dockerfile)
	}
}

func TestIsAcrImageReference(t *testing.T) {
	if !isAcrImageReference("myregistry.azurecr.io/echo_env:latest") {
		t.Fatal("expected ACR image reference")
	}
	if isAcrImageReference("echo_env:latest") {
		t.Fatal("did not expect local image tag to be treated as ACR")
	}
}

func TestLocalRuntimeImageForRunDefaultsToSourceFolder(t *testing.T) {
	tempDir := filepath.Join(t.TempDir(), "My Env")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		t.Fatal(err)
	}

	image := localRuntimeImageForRun(
		&localRunFlags{source: tempDir},
		rleState{Name: defaultSourceName(tempDir)},
	)
	if image != "my-env:local" {
		t.Fatalf("expected source folder image, got %q", image)
	}
}

func TestResolveDockerBuildRejectsDockerfileEscapes(t *testing.T) {
	tempDir := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "Dockerfile"), []byte("FROM scratch\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, dockerfile := range []string{
		filepath.Join("..", filepath.Base(outsideDir), "Dockerfile"),
		filepath.Join(outsideDir, "Dockerfile"),
	} {
		if _, _, _, err := prepareDockerBuild(dockerBuildOptions{
			source:     tempDir,
			dockerfile: dockerfile,
		}); err == nil {
			t.Fatalf("expected Dockerfile path %q to be rejected", dockerfile)
		}
	}
}
