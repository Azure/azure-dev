// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"azure.ai.rle/internal/ui"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const testFoundryProjectPath = "/api/projects/project-1"

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
		case r.Method == http.MethodGet &&
			r.URL.Path == testFoundryProjectPath+"/fine_tuning/environments/code_rl/versions/v1":
			_, _ = w.Write([]byte(`{"id":"env-1","version":"v1","diskImageConversionStatus":"Ready"}`))
		case r.Method == http.MethodPost &&
			r.URL.Path == testFoundryProjectPath+"/fine_tuning/environments/env-1/sandboxes/lease":
			if err := json.NewDecoder(r.Body).Decode(&sandboxBody); err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte(`{"id":"sandbox-1","status":"Running","baseUrl":` + strconv.Quote(envServer.URL) + `}`))
		case r.Method == http.MethodDelete &&
			r.URL.Path == testFoundryProjectPath+"/fine_tuning/environments/env-1/sandboxes/sandbox-1/release":
			deleteCalled = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected sandbox request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer controlPlane.Close()
	useTestProjectEndpoint(t, controlPlane.URL)

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
	if !strings.Contains(output.String(), "Sandbox sandbox-1 ready") {
		t.Fatalf("expected sandbox ready output, got %s", output.String())
	}
	if strings.Contains(output.String(), envServer.URL) {
		t.Fatalf("expected sandbox data-plane URL to remain hidden, got %s", output.String())
	}
	if !strings.Contains(output.String(), `"status": "healthy"`) {
		t.Fatalf("expected remote shell health output, got %s", output.String())
	}
	if !deleteCalled {
		t.Fatal("expected remote invoke to release the sandbox")
	}
}

func TestInvokeRemoteByNameUsesLatestListedVersionWithoutLocalState(t *testing.T) {
	captureBrowserOpen(t)
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(
		foundryProjectEndpointEnvVar,
		"https://account.services.ai.azure.com/api/projects/project-1",
	)

	envServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"status":"healthy"}`))
		case "/web":
			_, _ = w.Write([]byte("<html>environment</html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer envServer.Close()

	var sandboxBody sandboxCreateRequest
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet &&
			r.URL.Path == testFoundryProjectPath+environmentCollectionPath:
			_, _ = w.Write([]byte(`{
				"value": [{"id":"env-2","name":"code_rl","version":"2.0.0","diskImageConversionStatus":"Ready"}]
			}`))
		case r.Method == http.MethodGet &&
			r.URL.Path == testFoundryProjectPath+"/fine_tuning/environments/code_rl/versions/2.0.0":
			_, _ = w.Write([]byte(`{"id":"env-2","name":"code_rl","version":"2.0.0","diskImageConversionStatus":"Ready"}`))
		case r.Method == http.MethodPost &&
			r.URL.Path == testFoundryProjectPath+"/fine_tuning/environments/env-2/sandboxes/lease":
			if err := json.NewDecoder(r.Body).Decode(&sandboxBody); err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte(`{"id":"sandbox-1","status":"Running","baseUrl":` + strconv.Quote(envServer.URL) + `}`))
		case r.Method == http.MethodDelete &&
			r.URL.Path == testFoundryProjectPath+"/fine_tuning/environments/env-2/sandboxes/sandbox-1/release":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer controlPlane.Close()
	stubRleClientEndpoint(t, controlPlane.URL)

	command := newInvokeCommand()
	command.SetArgs([]string{"code_rl"})
	command.SetIn(strings.NewReader("exit\n"))
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if sandboxBody.Version != "2.0.0" {
		t.Fatalf("expected latest listed version, got %q", sandboxBody.Version)
	}
	if !strings.Contains(output.String(), "Creating sandbox for environment code_rl version 2.0.0") {
		t.Fatalf("expected resolved environment output, got %s", output.String())
	}
	if _, err := os.Stat(stateFilePath(".")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected cloud-only invoke not to create local state, got %v", err)
	}
}

func TestInvokeRemoteByNameFailsWhenLatestDiskImageIsNotReady(t *testing.T) {
	captureBrowserOpen(t)
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(
		foundryProjectEndpointEnvVar,
		"https://account.services.ai.azure.com/api/projects/project-1",
	)

	leaseCalled := false
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet &&
			r.URL.Path == testFoundryProjectPath+environmentCollectionPath:
			_, _ = w.Write([]byte(`{
				"value": [{"id":"env-2","name":"code_rl","version":"2.0.0","diskImageConversionStatus":"Pending"}]
			}`))
		case r.Method == http.MethodPost &&
			strings.Contains(r.URL.Path, "/sandboxes/lease"):
			leaseCalled = true
			t.Fatalf("expected invoke to stop before leasing a sandbox")
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer controlPlane.Close()
	stubRleClientEndpoint(t, controlPlane.URL)

	command := newInvokeCommand()
	command.SetArgs([]string{"code_rl"})
	command.SetIn(strings.NewReader("exit\n"))
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	err := command.Execute()
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T: %v", err, err)
	}
	if localErr.Code != "rle_disk_image_not_ready" {
		t.Fatalf("expected disk image not ready code, got %q", localErr.Code)
	}
	if localErr.Message != `Environment "code_rl" disk image status is "Pending", expected "Ready".` {
		t.Fatalf("unexpected disk image error message: %q", localErr.Message)
	}
	if localErr.Suggestion != "Run azd ai rle show code_rl to inspect the environment details and version history." {
		t.Fatalf("unexpected disk image error suggestion: %q", localErr.Suggestion)
	}
	if leaseCalled {
		t.Fatal("expected invoke not to lease sandbox when disk image is not ready")
	}
}

func TestInvokeRemoteByNameUsesExplicitVersion(t *testing.T) {
	captureBrowserOpen(t)
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(
		foundryProjectEndpointEnvVar,
		"https://account.services.ai.azure.com/api/projects/project-1",
	)

	envServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"status":"healthy"}`))
		case "/web":
			_, _ = w.Write([]byte("<html>environment</html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer envServer.Close()

	var sandboxBody sandboxCreateRequest
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet &&
			r.URL.Path == testFoundryProjectPath+environmentCollectionPath:
			_, _ = w.Write([]byte(`{
				"value": [{"id":"env-2","name":"code_rl","version":"2.0.0","diskImageConversionStatus":"Ready"}]
			}`))
		case r.Method == http.MethodGet &&
			r.URL.Path == testFoundryProjectPath+"/fine_tuning/environments/code_rl/versions/1.0.0":
			_, _ = w.Write([]byte(`{"id":"env-1","name":"code_rl","version":"1.0.0","diskImageConversionStatus":"Ready"}`))
		case r.Method == http.MethodPost &&
			r.URL.Path == testFoundryProjectPath+"/fine_tuning/environments/env-1/sandboxes/lease":
			if err := json.NewDecoder(r.Body).Decode(&sandboxBody); err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte(`{"id":"sandbox-1","status":"Running","baseUrl":` + strconv.Quote(envServer.URL) + `}`))
		case r.Method == http.MethodDelete &&
			r.URL.Path == testFoundryProjectPath+"/fine_tuning/environments/env-1/sandboxes/sandbox-1/release":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer controlPlane.Close()
	stubRleClientEndpoint(t, controlPlane.URL)

	command := newInvokeCommand()
	command.SetArgs([]string{"code_rl", "--version", "1.0.0"})
	command.SetIn(strings.NewReader("exit\n"))
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if sandboxBody.Version != "1.0.0" {
		t.Fatalf("expected explicit version, got %q", sandboxBody.Version)
	}
}

func TestInvokeRemoteByNameClassifiesListFailuresAsServiceErrors(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(
		foundryProjectEndpointEnvVar,
		"https://account.services.ai.azure.com/api/projects/project-1",
	)

	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}))
	defer controlPlane.Close()
	stubRleClientEndpoint(t, controlPlane.URL)

	command := newInvokeCommand()
	command.SetArgs([]string{"code_rl"})
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	err := command.Execute()
	serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
	if !ok {
		t.Fatalf("expected ServiceError, got %T: %v", err, err)
	}
	if serviceErr.ServiceName != "rle-control-plane" {
		t.Fatalf("expected rle-control-plane service, got %q", serviceErr.ServiceName)
	}
}

func TestInvokeRemoteByNameClassifiesVersionFailuresAsServiceErrors(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(
		foundryProjectEndpointEnvVar,
		"https://account.services.ai.azure.com/api/projects/project-1",
	)

	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == testFoundryProjectPath+environmentCollectionPath:
			_, _ = w.Write([]byte(`{
				"value": [{"id":"env-2","name":"code_rl","version":"2.0.0","diskImageConversionStatus":"Ready"}]
			}`))
		case r.Method == http.MethodGet &&
			r.URL.Path == testFoundryProjectPath+environmentCollectionPath+"/code_rl/versions/1.0.0":
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer controlPlane.Close()
	stubRleClientEndpoint(t, controlPlane.URL)

	command := newInvokeCommand()
	command.SetArgs([]string{"code_rl", "--version", "1.0.0"})
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	err := command.Execute()
	serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
	if !ok {
		t.Fatalf("expected ServiceError, got %T: %v", err, err)
	}
	if serviceErr.ServiceName != "rle-control-plane" {
		t.Fatalf("expected rle-control-plane service, got %q", serviceErr.ServiceName)
	}
}

func TestInvokeRemoteRejectsVersionWithoutEnvironmentName(t *testing.T) {
	command := newInvokeCommand()
	command.SetArgs([]string{"--version", "1.0.0"})
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)

	err := command.Execute()
	var localErr *azdext.LocalError
	if !errors.As(err, &localErr) {
		t.Fatalf("expected local error, got %v", err)
	}
	if localErr.Code != "rle_environment_name_required" {
		t.Fatalf("expected environment-name-required error, got %q", localErr.Code)
	}
}

func TestInvokeRemoteUsesSandboxWebWhenAvailable(t *testing.T) {
	openedUrl := captureBrowserOpen(t)
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	if err := saveRleState(rleState{
		Name:               "code_rl",
		ProjectEndpoint:    "https://account.services.ai.azure.com/api/projects/project-1",
		EnvironmentId:      "env-1",
		EnvironmentVersion: "1.0.0",
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
		case r.Method == http.MethodGet &&
			r.URL.Path == testFoundryProjectPath+"/fine_tuning/environments/code_rl/versions/1.0.0":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"env-1","version":"1.0.0","diskImageConversionStatus":"Ready"}`))
		case r.Method == http.MethodPost &&
			r.URL.Path == testFoundryProjectPath+"/fine_tuning/environments/env-1/sandboxes/lease":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sandbox-1","status":"Running","baseUrl":` + strconv.Quote(envServer.URL) + `}`))
		case r.Method == http.MethodDelete &&
			r.URL.Path == testFoundryProjectPath+"/fine_tuning/environments/env-1/sandboxes/sandbox-1/release":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected sandbox request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer controlPlane.Close()
	useTestProjectEndpoint(t, controlPlane.URL)

	command := newInvokeCommand()
	command.SetIn(strings.NewReader("exit\n"))
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "Playground UI:") {
		t.Fatalf("expected playground URL to remain hidden, got %s", output.String())
	}
	if strings.Contains(output.String(), envServer.URL+"/web") {
		t.Fatalf("expected playground proxy URL to remain hidden, got %s", output.String())
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

	ctx := t.Context()
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
		Name:               "code_rl",
		ProjectEndpoint:    "https://account.services.ai.azure.com/api/projects/project-1",
		EnvironmentId:      "env-1",
		EnvironmentVersion: "1.0.0",
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
		case r.Method == http.MethodGet &&
			r.URL.Path == testFoundryProjectPath+"/fine_tuning/environments/code_rl/versions/1.0.0":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"env-1","version":"1.0.0","diskImageConversionStatus":"Ready"}`))
		case r.Method == http.MethodPost &&
			r.URL.Path == testFoundryProjectPath+"/fine_tuning/environments/env-1/sandboxes/lease":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sandbox-1","status":"Starting"}`))
		case r.Method == http.MethodGet &&
			r.URL.Path == testFoundryProjectPath+"/fine_tuning/environments/env-1/sandboxes/sandbox-1":
			getCount++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sandbox-1","status":"Running","baseUrl":` + strconv.Quote(envServer.URL) + `}`))
		case r.Method == http.MethodDelete &&
			r.URL.Path == testFoundryProjectPath+"/fine_tuning/environments/env-1/sandboxes/sandbox-1/release":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected sandbox request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer controlPlane.Close()
	useTestProjectEndpoint(t, controlPlane.URL)

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
	if !strings.Contains(output.String(), "Sandbox sandbox-1 ready") {
		t.Fatalf("expected sandbox ready output, got %s", output.String())
	}
	if strings.Contains(output.String(), envServer.URL) {
		t.Fatalf("expected sandbox data-plane URL to remain hidden, got %s", output.String())
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
			r.URL.Path == testFoundryProjectPath+"/fine_tuning/environments/env-1/sandboxes/lease":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sandbox-1","status":"Failed","error":"image pull failed"}`))
		case r.Method == http.MethodDelete &&
			r.URL.Path == testFoundryProjectPath+"/fine_tuning/environments/env-1/sandboxes/sandbox-1/release":
			deleteCalled = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected sandbox request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer controlPlane.Close()
	useTestProjectEndpoint(t, controlPlane.URL)

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
	listener, err := net.Listen("tcp", ":0") //nolint:gosec // test intentionally binds an ephemeral port on all interfaces to verify conflict detection.
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
	if err := os.MkdirAll(tempDir, 0750); err != nil {
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
		Name:               "code_rl",
		ProjectEndpoint:    "https://account.services.ai.azure.com/api/projects/project-1",
		EnvironmentId:      "env-1",
		EnvironmentVersion: "1.0.0",
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

	conversionGetCount := 0
	createCount := 0
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet &&
			r.URL.Path == testFoundryProjectPath+"/fine_tuning/environments/code_rl/versions/1.0.0":
			conversionGetCount++
			w.Header().Set("Content-Type", "application/json")
			status := "Pending"
			if conversionGetCount >= 3 {
				status = "Ready"
			}
			_, _ = fmt.Fprintf(w, `{"id":"env-1","version":"1.0.0","diskImageConversionStatus":%q}`, status)
		case r.Method == http.MethodPost &&
			r.URL.Path == testFoundryProjectPath+"/fine_tuning/environments/env-1/sandboxes/lease":
			createCount++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sandbox-1","status":"Running","baseUrl":` + strconv.Quote(envServer.URL) + `}`))
		case r.Method == http.MethodDelete &&
			r.URL.Path == testFoundryProjectPath+"/fine_tuning/environments/env-1/sandboxes/sandbox-1/release":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected sandbox request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer controlPlane.Close()
	useTestProjectEndpoint(t, controlPlane.URL)

	command := newInvokeCommand()
	command.SetIn(strings.NewReader("exit\n"))
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if conversionGetCount != 3 {
		t.Fatalf("expected three conversion status checks, got %d", conversionGetCount)
	}
	if createCount != 1 {
		t.Fatalf("expected one sandbox lease after conversion, got %d", createCount)
	}
	if !strings.Contains(output.String(), "Preparing environment disk image (status: Pending); waiting") {
		t.Fatalf("expected disk image conversion wait message, got %s", output.String())
	}
	if !strings.Contains(output.String(), "Sandbox sandbox-1 ready") {
		t.Fatalf("expected sandbox ready output, got %s", output.String())
	}
	if strings.Contains(output.String(), envServer.URL) {
		t.Fatalf("expected sandbox data-plane URL to remain hidden, got %s", output.String())
	}
}

func TestRemoteInvokeDoesNotRetrySandboxLeaseConflictsAfterImageReady(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	if err := saveRleState(rleState{
		Name:               "code_rl",
		ProjectEndpoint:    "https://account.services.ai.azure.com/api/projects/project-1",
		EnvironmentId:      "env-1",
		EnvironmentVersion: "1.0.0",
	}); err != nil {
		t.Fatal(err)
	}

	createCount := 0
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet &&
			r.URL.Path == testFoundryProjectPath+"/fine_tuning/environments/code_rl/versions/1.0.0" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"env-1","version":"1.0.0","diskImageConversionStatus":"Ready"}`))
			return
		}
		if r.Method == http.MethodPost &&
			r.URL.Path == testFoundryProjectPath+"/fine_tuning/environments/env-1/sandboxes/lease" {
			createCount++
			http.Error(w, `{"error":"quota unavailable"}`, http.StatusConflict)
			return
		}
		t.Fatalf("unexpected sandbox request: %s %s", r.Method, r.URL.Path)
	}))
	defer controlPlane.Close()
	useTestProjectEndpoint(t, controlPlane.URL)

	command := newInvokeCommand()
	command.SetIn(strings.NewReader("exit\n"))
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	err := command.Execute()
	if err == nil {
		t.Fatal("expected sandbox lease conflict")
	}
	serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
	if !ok {
		t.Fatalf("expected ServiceError, got %T", err)
	}
	if createCount != 1 {
		t.Fatalf("expected one sandbox lease attempt, got %d", createCount)
	}
	if !strings.Contains(serviceErr.Message, "quota unavailable") {
		t.Fatalf("expected lease conflict details, got %q", serviceErr.Message)
	}
}

func TestWaitForEnvironmentImageReportsConversionFailure(t *testing.T) {
	client := newRleClientWithCredential("https://rle.test", &testTokenCredential{})
	client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(
				`{"diskImageConversionStatus":"Failed","diskImageConversionError":"invalid image"}`,
			)),
			Header: make(http.Header),
		}, nil
	})

	err := waitForEnvironmentImage(t.Context(), io.Discard, client, rleState{
		Name:               "code_rl",
		EnvironmentVersion: "1.0.0",
	})
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T", err)
	}
	if localErr.Code != "rle_disk_image_conversion_failed" ||
		!strings.Contains(localErr.Message, "invalid image") {
		t.Fatalf("expected conversion failure details, got %#v", localErr)
	}
}

func captureBrowserOpen(t *testing.T) *string {
	t.Helper()
	old := ui.OpenBrowser
	openedUrl := ""
	ui.OpenBrowser = func(url string) error {
		openedUrl = url
		return nil
	}
	t.Cleanup(func() {
		ui.OpenBrowser = old
	})
	return &openedUrl
}

func useTestProjectEndpoint(t *testing.T, endpoint string) {
	t.Helper()
	target, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	oldCreateRleClient := createRleClient
	createRleClient = func(string) (*rleClient, error) {
		client := newRleClientWithCredential(
			"https://rle.test"+testFoundryProjectPath,
			&testTokenCredential{},
		)
		client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
			request = request.Clone(request.Context())
			request.URL.Scheme = target.Scheme
			request.URL.Host = target.Host
			return http.DefaultTransport.RoundTrip(request)
		})
		return client, nil
	}
	t.Cleanup(func() {
		createRleClient = oldCreateRleClient
	})

	state, err := loadRleState()
	if err != nil {
		t.Fatal(err)
	}
	state.ProjectEndpoint = endpoint
	if err := saveRleState(state); err != nil {
		t.Fatal(err)
	}
}

func TestResolvePublishStateDefaultsToExistingFolderWithoutInit(t *testing.T) {
	tempDir := filepath.Join(t.TempDir(), "My Env")
	if err := os.MkdirAll(tempDir, 0750); err != nil {
		t.Fatal(err)
	}
	t.Chdir(tempDir)
	t.Setenv(foundryProjectEndpointEnvVar, "https://account.services.ai.azure.com/api/projects/project-1")

	state, initialized, err := resolvePublishState()
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

func TestResolvePublishImageUsesTerminalAcrRegistryEnvironment(t *testing.T) {
	t.Setenv("AZURE_CONTAINER_REGISTRY_ENDPOINT", "example.azurecr.io")

	image, err := resolvePublishImage(
		rleState{Name: "My Env", ProjectEndpoint: "https://account.services.ai.azure.com/api/projects/Project 1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if image != "example.azurecr.io/project-1-my-env:latest" {
		t.Fatalf("expected derived ACR image, got %q", image)
	}
}

func TestResolvePublishImageRequiresAcrRegistry(t *testing.T) {
	_, err := resolvePublishImage(
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

func TestResolvePublishImageUsesRegistryEvenWhenStateExists(t *testing.T) {
	t.Setenv("AZURE_CONTAINER_REGISTRY_ENDPOINT", "example.azurecr.io")

	image, err := resolvePublishImage(
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

func TestLocalRuntimeImageForRunDefaultsToSourceFolder(t *testing.T) {
	tempDir := filepath.Join(t.TempDir(), "My Env")
	if err := os.MkdirAll(tempDir, 0750); err != nil {
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
