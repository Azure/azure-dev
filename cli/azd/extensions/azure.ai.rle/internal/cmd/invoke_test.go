// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"azure.ai.rle/internal/ui"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const testFoundryProjectPath = "/api/projects/project-1"

func TestInvokeRemoteCreatesInstanceAndRunsShell(t *testing.T) {
	captureBrowserOpen(t)
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	if err := saveRleState(rleState{
		EnvironmentName:    "code_rl",
		ProjectEndpoint:    "https://account.services.ai.azure.com/api/projects/project-1",
		EnvironmentId:      "env-1",
		EnvironmentVersion: "v1",
	}); err != nil {
		t.Fatal(err)
	}

	envServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("api-version"); got != foundryAPIVersion {
			t.Errorf("expected OpenEnv API version %q, got %q", foundryAPIVersion, got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"status":"healthy"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer envServer.Close()

	instanceDeleted := false
	groupDeleted := false
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost &&
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/versions/v1/instance_groups":
			_, _ = w.Write([]byte(
				`{"id":"group-1","environmentName":"code_rl",` +
					`"environmentVersion":"v1","maxActiveInstances":1}`,
			))
		case r.Method == http.MethodPost &&
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/versions/v1/instance_groups/group-1/instances":
			_, _ = w.Write([]byte(
				`{"instanceId":"instance-1","instanceGroupId":"group-1","status":"Running","baseUrl":` +
					strconv.Quote(envServer.URL) + `}`,
			))
		case r.Method == http.MethodDelete &&
			r.URL.Path == testFoundryProjectPath+
				"/rl_environments/code_rl/versions/v1/instance_groups/group-1/instances/instance-1":
			instanceDeleted = true
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete &&
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/versions/v1/instance_groups/group-1":
			groupDeleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected instance request: %s %s", r.Method, r.URL.Path)
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
	if !strings.Contains(output.String(), "Environment code_rl version v1 ready") {
		t.Fatalf("expected environment ready output, got %s", output.String())
	}
	if strings.Contains(output.String(), envServer.URL) {
		t.Fatalf("expected instance data-plane URL to remain hidden, got %s", output.String())
	}
	if !strings.Contains(output.String(), `"status": "healthy"`) {
		t.Fatalf("expected remote shell health output, got %s", output.String())
	}
	if !instanceDeleted || !groupDeleted {
		t.Fatal("expected remote invoke to delete the instance and group")
	}
}

func TestValidateRemoteSandboxURLRequiresTrustedOrigin(t *testing.T) {
	tests := []struct {
		name            string
		projectEndpoint string
		sandboxUrl      string
		wantError       bool
	}{
		{
			name:            "accepts RLE data proxy on project origin",
			projectEndpoint: "https://account.services.ai.azure.com/api/projects/project-1",
			sandboxUrl:      "https://account.services.ai.azure.com/rle/v1.0/subscriptions/sub/sandboxes/sandbox-1/openenv",
		},
		{
			name:            "rejects Hyena runtime host",
			projectEndpoint: "https://account.services.ai.azure.com/api/projects/project-1",
			sandboxUrl:      "https://rle.westus2.hyena.infra.ai.azure.com/subscriptions/sub/sandboxes/sandbox-1/openenv",
			wantError:       true,
		},
		{
			name:            "rejects loopback host",
			projectEndpoint: "https://account.services.ai.azure.com/api/projects/project-1",
			sandboxUrl:      "http://127.0.0.1:8080/openenv",
			wantError:       true,
		},
		{
			name:            "rejects matching custom port on project origin",
			projectEndpoint: "https://account.services.ai.azure.com:8443/api/projects/project-1",
			sandboxUrl:      "https://account.services.ai.azure.com:8443/openenv",
			wantError:       true,
		},
		{
			name:            "rejects embedded credentials",
			projectEndpoint: "https://account.services.ai.azure.com/api/projects/project-1",
			sandboxUrl:      "https://user@account.services.ai.azure.com/openenv",
			wantError:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRemoteSandboxURL(tt.sandboxUrl, tt.projectEndpoint)
			if tt.wantError && err == nil {
				t.Fatalf("expected %q to be rejected", tt.sandboxUrl)
			}
			if !tt.wantError && err != nil {
				t.Fatalf("expected %q to be accepted: %v", tt.sandboxUrl, err)
			}
		})
	}

	secret := "secret-value"
	err := validateRemoteSandboxURL(
		"https://user:"+secret+"@account.services.ai.azure.com/openenv",
		"https://account.services.ai.azure.com/api/projects/project-1",
	)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("expected embedded credentials to be rejected without disclosure, got %v", err)
	}
}

func TestCleanupRemoteRuntimeDeletesInstanceThenGroup(t *testing.T) {
	tests := []struct {
		name           string
		instanceStatus int
		groupStatus    int
		wantError      bool
	}{
		{
			name:           "success",
			instanceStatus: http.StatusNoContent,
			groupStatus:    http.StatusNoContent,
		},
		{
			name:           "not found is already cleaned",
			instanceStatus: http.StatusNotFound,
			groupStatus:    http.StatusNotFound,
		},
		{
			name:           "instance failure still deletes group",
			instanceStatus: http.StatusInternalServerError,
			groupStatus:    http.StatusNoContent,
			wantError:      true,
		},
		{
			name:           "group failure is reported",
			instanceStatus: http.StatusNoContent,
			groupStatus:    http.StatusInternalServerError,
			wantError:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests []string
			client := newRleClientWithCredential(
				"https://account.services.ai.azure.com/api/projects/project-1",
				&testTokenCredential{},
			)
			client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.Context().Err() != nil {
					t.Fatalf("expected independent cleanup context, got %v", request.Context().Err())
				}
				requests = append(requests, request.Method+" "+request.URL.Path)
				status := tt.instanceStatus
				if strings.HasSuffix(request.URL.Path, "/group-1") {
					status = tt.groupStatus
				}
				return &http.Response{
					StatusCode: status,
					Body:       io.NopCloser(strings.NewReader("{}")),
					Header:     make(http.Header),
				}, nil
			})

			err := cleanupRemoteRuntime(client, "code_rl", &remoteRuntime{
				group: &instanceGroupResource{
					Id:                 "group-1",
					EnvironmentVersion: "1.0.0",
				},
				instance: &instanceResource{InstanceId: "instance-1"},
			})
			if (err != nil) != tt.wantError {
				t.Fatalf("expected error=%t, got %v", tt.wantError, err)
			}
			expected := []string{
				"DELETE /api/projects/project-1/rl_environments/code_rl/versions/1.0.0/instance_groups/group-1/" +
					"instances/instance-1",
				"DELETE /api/projects/project-1/rl_environments/code_rl/versions/1.0.0/instance_groups/group-1",
			}
			if !slices.Equal(requests, expected) {
				t.Fatalf("expected cleanup requests %v, got %v", expected, requests)
			}
		})
	}
}

func TestWriteCleanupResultDoesNotExposeResourceDetails(t *testing.T) {
	var output bytes.Buffer
	writeCleanupResult(&output, nil)
	if output.String() != "Remote runtime resources cleaned up successfully.\n" {
		t.Fatalf("unexpected successful cleanup output: %q", output.String())
	}

	output.Reset()
	writeCleanupResult(&output, errors.New("instance instance-1 in group group-1 failed"))
	if output.String() != "Warning: remote runtime cleanup could not be completed; resources may remain.\n" {
		t.Fatalf("unexpected failed cleanup output: %q", output.String())
	}
}

func TestInvokeRemoteFromStateDoesNotFallBackInstanceGroupsToLegacyPrefix(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := saveRleState(rleState{
		EnvironmentName:    "code_rl",
		ProjectEndpoint:    "https://account.services.ai.azure.com/api/projects/project-1",
		EnvironmentId:      "env-1",
		EnvironmentVersion: "v1",
	}); err != nil {
		t.Fatal(err)
	}

	requestCount := 0
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.Method != http.MethodPost ||
			r.URL.Path != testFoundryProjectPath+"/rl_environments/code_rl/versions/v1/instance_groups" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		http.NotFound(w, r)
	}))
	defer controlPlane.Close()
	useTestProjectEndpoint(t, controlPlane.URL)

	command := newInvokeCommand()
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	err := command.Execute()
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T: %v", err, err)
	}
	if localErr.Code != "rle_environment_version_not_found" {
		t.Fatalf("expected version-not-found code, got %q", localErr.Code)
	}
	if requestCount != 1 {
		t.Fatalf("expected one primary instance-group request, got %d", requestCount)
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

	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost &&
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/instance_groups":
			_, _ = w.Write([]byte(
				`{"id":"group-1","environmentName":"code_rl",` +
					`"environmentVersion":"2.0.0","maxActiveInstances":1}`,
			))
		case r.Method == http.MethodPost &&
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/versions/2.0.0/instance_groups/group-1/instances":
			_, _ = w.Write([]byte(
				`{"instanceId":"instance-1","instanceGroupId":"group-1","status":"Running","baseUrl":` +
					strconv.Quote(envServer.URL) + `}`,
			))
		case r.Method == http.MethodDelete &&
			r.URL.Path == testFoundryProjectPath+
				"/rl_environments/code_rl/versions/2.0.0/instance_groups/group-1/instances/instance-1":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete &&
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/versions/2.0.0/instance_groups/group-1":
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
	if !strings.Contains(output.String(), "Creating runtime for environment code_rl using the latest version") {
		t.Fatalf("expected latest-version runtime output, got %s", output.String())
	}
	if _, err := os.Stat(stateFilePath(".")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected cloud-only invoke not to create local state, got %v", err)
	}
}

func TestInvokeRemoteByNameTimesOutWhileWaitingForEnvironmentReadiness(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(
		foundryProjectEndpointEnvVar,
		"https://account.services.ai.azure.com/api/projects/project-1",
	)

	oldRetryInterval := remoteReadinessRetryInterval
	remoteReadinessRetryInterval = time.Millisecond
	defer func() { remoteReadinessRetryInterval = oldRetryInterval }()

	groupCreateCount := 0
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost ||
			r.URL.Path != testFoundryProjectPath+"/rl_environments/code_rl/instance_groups" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		groupCreateCount++
		http.Error(
			w,
			`{"code":"EnvironmentNotReady","message":"The environment disk image is not ready."}`,
			http.StatusBadRequest,
		)
	}))
	defer controlPlane.Close()
	stubRleClientEndpoint(t, controlPlane.URL)

	command := newInvokeCommand()
	command.SetArgs([]string{"code_rl"})
	command.SetIn(strings.NewReader("exit\n"))
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	err := command.Execute()
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T: %v", err, err)
	}
	if localErr.Code != "rle_environment_readiness_timeout" {
		t.Fatalf("expected readiness timeout code, got %q", localErr.Code)
	}
	if groupCreateCount != remoteReadinessRetryCount+1 {
		t.Fatalf("expected %d readiness attempts, got %d", remoteReadinessRetryCount+1, groupCreateCount)
	}
	if !strings.Contains(
		output.String(),
		"The requested environment's disk image is not ready yet. Waiting 0 seconds before retrying (10/10) ...",
	) {
		t.Fatalf("expected readiness progress output, got %s", output.String())
	}
}

func TestInvokeRemoteByNameRetriesEnvironmentReadinessUntilSuccess(t *testing.T) {
	captureBrowserOpen(t)
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(
		foundryProjectEndpointEnvVar,
		"https://account.services.ai.azure.com/api/projects/project-1",
	)

	oldRetryInterval := remoteReadinessRetryInterval
	remoteReadinessRetryInterval = time.Millisecond
	defer func() { remoteReadinessRetryInterval = oldRetryInterval }()

	envServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"status":"healthy"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer envServer.Close()

	groupCreateCount := 0
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost &&
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/instance_groups":
			groupCreateCount++
			if groupCreateCount < 3 {
				http.Error(
					w,
					`{"code":"EnvironmentNotReady","message":"The environment disk image is not ready."}`,
					http.StatusBadRequest,
				)
				return
			}
			_, _ = w.Write([]byte(
				`{"id":"group-1","environmentName":"code_rl",` +
					`"environmentVersion":"2.0.0","maxActiveInstances":1}`,
			))
		case r.Method == http.MethodPost &&
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/versions/2.0.0/instance_groups/group-1/instances":
			_, _ = w.Write([]byte(
				`{"instanceId":"instance-1","instanceGroupId":"group-1","status":"Running","baseUrl":` +
					strconv.Quote(envServer.URL) + `}`,
			))
		case r.Method == http.MethodDelete &&
			r.URL.Path == testFoundryProjectPath+
				"/rl_environments/code_rl/versions/2.0.0/instance_groups/group-1/instances/instance-1":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete &&
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/versions/2.0.0/instance_groups/group-1":
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
	if groupCreateCount != 3 {
		t.Fatalf("expected three group-creation attempts, got %d", groupCreateCount)
	}
	if !strings.Contains(
		output.String(),
		"The requested environment's disk image is not ready yet. Waiting 0 seconds before retrying (2/10) ...",
	) {
		t.Fatalf("expected readiness retry output, got %s", output.String())
	}
	if !strings.Contains(output.String(), "Environment code_rl version 2.0.0 ready") {
		t.Fatalf("expected eventual success output, got %s", output.String())
	}
}

func TestInvokeRemoteDoesNotReportReadyBeforeRuntimeHealthSucceeds(t *testing.T) {
	captureBrowserOpen(t)
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(
		foundryProjectEndpointEnvVar,
		"https://account.services.ai.azure.com/api/projects/project-1",
	)

	oldHealthTimeout := remoteRuntimeHealthTimeout
	remoteRuntimeHealthTimeout = 10 * time.Millisecond
	defer func() { remoteRuntimeHealthTimeout = oldHealthTimeout }()

	envServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"code":"RuntimeStarting","message":"Runtime is starting."}}`, http.StatusServiceUnavailable)
	}))
	defer envServer.Close()

	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost &&
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/instance_groups":
			_, _ = w.Write([]byte(
				`{"id":"group-1","environmentName":"code_rl",` +
					`"environmentVersion":"2.0.0","maxActiveInstances":1}`,
			))
		case r.Method == http.MethodPost &&
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/versions/2.0.0/instance_groups/group-1/instances":
			_, _ = w.Write([]byte(
				`{"instanceId":"instance-1","instanceGroupId":"group-1","status":"Running","baseUrl":` +
					strconv.Quote(envServer.URL) + `}`,
			))
		case r.Method == http.MethodDelete &&
			r.URL.Path == testFoundryProjectPath+
				"/rl_environments/code_rl/versions/2.0.0/instance_groups/group-1/instances/instance-1":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete &&
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/versions/2.0.0/instance_groups/group-1":
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
	err := command.Execute()
	if err == nil {
		t.Fatal("expected runtime health failure")
	}
	if !strings.Contains(output.String(), "Environment instance is running; waiting for OpenEnv runtime ...") {
		t.Fatalf("expected runtime health wait output, got %s", output.String())
	}
	if strings.Contains(output.String(), "Environment code_rl version 2.0.0 ready") {
		t.Fatalf("did not expect ready output before runtime health succeeded, got %s", output.String())
	}
	if !strings.Contains(err.Error(), "Runtime is starting.") {
		t.Fatalf("expected runtime health response detail, got %v", err)
	}
}

func TestInvokeRemoteByNameUsesExplicitVersionInstanceRoutes(t *testing.T) {
	captureBrowserOpen(t)
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(
		foundryProjectEndpointEnvVar,
		"https://account.services.ai.azure.com/api/projects/project-1",
	)

	oldRetryInterval := remoteReadinessRetryInterval
	remoteReadinessRetryInterval = time.Millisecond
	defer func() { remoteReadinessRetryInterval = oldRetryInterval }()

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

	groupCreateCount := 0
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet &&
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/versions/1.0.0":
			_, _ = w.Write([]byte(`{"id":"env-1","name":"code_rl","version":"1.0.0","diskImageConversionStatus":"Pending"}`))
		case r.Method == http.MethodPost &&
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/versions/1.0.0/instance_groups":
			groupCreateCount++
			if groupCreateCount == 1 {
				http.Error(
					w,
					`{"code":"EnvironmentNotReady","message":"The environment disk image is not ready."}`,
					http.StatusBadRequest,
				)
				return
			}
			_, _ = w.Write([]byte(
				`{"id":"group-1","environmentName":"code_rl",` +
					`"environmentVersion":"1.0.0","maxActiveInstances":1}`,
			))
		case r.Method == http.MethodPost &&
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/versions/1.0.0/instance_groups/group-1/instances":
			_, _ = w.Write([]byte(
				`{"instanceId":"instance-1","instanceGroupId":"group-1","status":"Running","baseUrl":` +
					strconv.Quote(envServer.URL) + `}`,
			))
		case r.Method == http.MethodDelete &&
			r.URL.Path == testFoundryProjectPath+
				"/rl_environments/code_rl/versions/1.0.0/instance_groups/group-1/instances/instance-1":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete &&
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/versions/1.0.0/instance_groups/group-1":
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
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Creating runtime for environment code_rl version 1.0.0") {
		t.Fatalf("expected explicit version in runtime output, got %s", output.String())
	}
	if !strings.Contains(output.String(), "Environment code_rl version 1.0.0 ready") {
		t.Fatalf("expected explicit version in ready output, got %s", output.String())
	}
	if groupCreateCount != 2 {
		t.Fatalf("expected explicit version to retry instance-group creation, got %d attempts", groupCreateCount)
	}
}

func TestInvokeRemoteByNameDoesNotRetryOtherBadRequests(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(
		foundryProjectEndpointEnvVar,
		"https://account.services.ai.azure.com/api/projects/project-1",
	)

	groupCreateCount := 0
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost ||
			r.URL.Path != testFoundryProjectPath+"/rl_environments/code_rl/instance_groups" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		groupCreateCount++
		http.Error(w, `{"code":"InvalidRequest","message":"the request is invalid"}`, http.StatusBadRequest)
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
	if groupCreateCount != 1 {
		t.Fatalf("expected one bad-request attempt, got %d", groupCreateCount)
	}
	if !strings.Contains(serviceErr.Message, "the request is invalid") {
		t.Fatalf("expected original bad-request message, got %q", serviceErr.Message)
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
	if serviceErr.ServiceName != "rle-service" {
		t.Fatalf("expected rle-service, got %q", serviceErr.ServiceName)
	}
}

func TestInvokeRemoteByNameReportsMissingVersion(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(
		foundryProjectEndpointEnvVar,
		"https://account.services.ai.azure.com/api/projects/project-1",
	)

	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet ||
			r.URL.Path != testFoundryProjectPath+environmentCollectionPath+"/code_rl/versions/9.9.9" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		http.NotFound(w, r)
	}))
	defer controlPlane.Close()
	stubRleClientEndpoint(t, controlPlane.URL)

	command := newInvokeCommand()
	command.SetArgs([]string{"code_rl", "--version", "9.9.9"})
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	err := command.Execute()
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T: %v", err, err)
	}
	if localErr.Code != "rle_environment_version_not_found" {
		t.Fatalf("expected version-not-found code, got %q", localErr.Code)
	}
	if !strings.Contains(localErr.Suggestion, "azd ai rle show code_rl") {
		t.Fatalf("unexpected version-not-found suggestion: %q", localErr.Suggestion)
	}
}

func TestInvokeRemoteByNameReportsMissingEnvironment(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(
		foundryProjectEndpointEnvVar,
		"https://account.services.ai.azure.com/api/projects/project-1",
	)

	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost ||
			r.URL.Path != testFoundryProjectPath+environmentCollectionPath+"/missing_env/instance_groups" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		http.NotFound(w, r)
	}))
	defer controlPlane.Close()
	stubRleClientEndpoint(t, controlPlane.URL)

	command := newInvokeCommand()
	command.SetArgs([]string{"missing_env"})
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	err := command.Execute()
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T: %v", err, err)
	}
	if localErr.Code != "rle_environment_not_found" {
		t.Fatalf("expected environment-not-found code, got %q", localErr.Code)
	}
	if localErr.Suggestion != "Run azd ai rle list to see the available environments." {
		t.Fatalf("unexpected environment-not-found suggestion: %q", localErr.Suggestion)
	}
}

func TestInvokeRemoteByNameRejectsMismatchedVersionResponse(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(
		foundryProjectEndpointEnvVar,
		"https://account.services.ai.azure.com/api/projects/project-1",
	)

	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet ||
			r.URL.Path != testFoundryProjectPath+environmentCollectionPath+"/code_rl/versions/1.0.0" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(
			`{"id":"env-1","name":"code_rl","version":"2.0.0","diskImageConversionStatus":"Ready"}`,
		))
	}))
	defer controlPlane.Close()
	stubRleClientEndpoint(t, controlPlane.URL)

	command := newInvokeCommand()
	command.SetArgs([]string{"code_rl", "--version", "1.0.0"})
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	err := command.Execute()
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected local error, got %v", err)
	}
	if localErr.Code != "rle_environment_version_mismatch" {
		t.Fatalf("expected environment-version-mismatch error, got %q", localErr.Code)
	}
}

func TestInvokeRemoteRejectsVersionWithoutEnvironmentName(t *testing.T) {
	command := newInvokeCommand()
	command.SetArgs([]string{"--version", "1.0.0"})
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)

	err := command.Execute()
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected local error, got %v", err)
	}
	if localErr.Code != "rle_environment_name_required" {
		t.Fatalf("expected environment-name-required error, got %q", localErr.Code)
	}
}

func TestInvokeRemoteUsesAuthenticatedPlaygroundProxy(t *testing.T) {
	openedUrl := captureBrowserOpen(t)
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	if err := saveRleState(rleState{
		EnvironmentName:    "code_rl",
		ProjectEndpoint:    "https://account.services.ai.azure.com/api/projects/project-1",
		EnvironmentId:      "env-1",
		EnvironmentVersion: "1.0.0",
	}); err != nil {
		t.Fatal(err)
	}

	envServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("expected runtime request to include the bearer token")
		}
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
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/versions/1.0.0/instance_groups":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(
				`{"id":"group-1","environmentName":"code_rl",` +
					`"environmentVersion":"1.0.0","maxActiveInstances":1}`,
			))
		case r.Method == http.MethodPost &&
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/versions/1.0.0/instance_groups/group-1/instances":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(
				`{"instanceId":"instance-1","instanceGroupId":"group-1","status":"Running","baseUrl":` +
					strconv.Quote(envServer.URL) + `}`,
			))
		case r.Method == http.MethodDelete &&
			r.URL.Path == testFoundryProjectPath+
				"/rl_environments/code_rl/versions/1.0.0/instance_groups/group-1/instances/instance-1":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete &&
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/versions/1.0.0/instance_groups/group-1":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected instance request: %s %s", r.Method, r.URL.Path)
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
	opened, err := url.Parse(*openedUrl)
	if err != nil {
		t.Fatal(err)
	}
	if opened.Hostname() != "127.0.0.1" || opened.Path != "/web" {
		t.Fatalf("expected browser to open the authenticated loopback proxy, got %q", *openedUrl)
	}
	if strings.TrimSpace(opened.Query().Get("token")) == "" {
		t.Fatalf("expected browser bootstrap URL to include an authorization token, got %q", *openedUrl)
	}
}

func TestRemotePlaygroundProxyForwardsToSandbox(t *testing.T) {
	requestCount := 0
	envServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
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

	playgroundUrl, stop, err := remotePlaygroundUrlWithAuthorizationProvider(
		t.Context(),
		envServer.URL,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if !strings.Contains(playgroundUrl, "127.0.0.1") || !strings.Contains(playgroundUrl, "/web?token=") {
		t.Fatalf("expected local playground URL, got %q", playgroundUrl)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	resp, err := client.Get(playgroundUrl) //nolint:gosec // Test-only local proxy URL.
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "RLE Remote Console") {
		t.Fatalf("expected playground HTML after token bootstrap, got %s", body)
	}
	if resp.Request.URL.RawQuery != "" {
		t.Fatalf("expected bootstrap redirect to clear the token query, got %q", resp.Request.URL.String())
	}

	baseUrl := playgroundBaseURL(t, playgroundUrl)
	request, err := http.NewRequest(http.MethodGet, baseUrl+"/state", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", baseUrl)
	resp, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"step_count":3}` {
		t.Fatalf("expected proxied state body, got %s", body)
	}
	if requestCount != 1 {
		t.Fatalf("expected one authorized backend request, got %d", requestCount)
	}
}

func TestRemotePlaygroundProxyRefreshesAuthorizationForEachRequest(t *testing.T) {
	var authorizations []string
	envServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"step_count":3}`))
	}))
	defer envServer.Close()

	tokenNumber := 0
	authorizationProvider := func(context.Context) (string, error) {
		tokenNumber++
		return fmt.Sprintf("Bearer token-%d", tokenNumber), nil
	}
	playgroundUrl, stop, err := remotePlaygroundUrlWithAuthorizationProvider(
		t.Context(),
		envServer.URL,
		authorizationProvider,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	client, baseUrl, origin := newAuthorizedPlaygroundClient(t, playgroundUrl)
	for range 2 {
		request, err := http.NewRequest(http.MethodGet, baseUrl+"/state", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Origin", origin)
		resp, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}

	expected := []string{"Bearer token-1", "Bearer token-2"}
	if !slices.Equal(authorizations, expected) {
		t.Fatalf("expected refreshed authorization headers %v, got %v", expected, authorizations)
	}
}

func TestRemotePlaygroundProxyRejectsUnauthorizedRequests(t *testing.T) {
	backendRequests := 0
	envServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendRequests++
		_, _ = w.Write([]byte(`{"step_count":3}`))
	}))
	defer envServer.Close()

	playgroundUrl, stop, err := remotePlaygroundUrlWithAuthorizationProvider(
		t.Context(),
		envServer.URL,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	baseUrl := playgroundBaseURL(t, playgroundUrl)
	resp, err := http.Get(baseUrl + "/state") //nolint:gosec // Test-only local proxy URL.
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized status, got %d", resp.StatusCode)
	}
	if backendRequests != 0 {
		t.Fatalf("expected no backend requests, got %d", backendRequests)
	}
}

func TestRemotePlaygroundProxyRejectsInvalidHostAndOrigin(t *testing.T) {
	backendRequests := 0
	envServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendRequests++
		_, _ = w.Write([]byte(`{"step_count":3}`))
	}))
	defer envServer.Close()

	playgroundUrl, stop, err := remotePlaygroundUrlWithAuthorizationProvider(
		t.Context(),
		envServer.URL,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	client, baseUrl, _ := newAuthorizedPlaygroundClient(t, playgroundUrl)
	for _, test := range []struct {
		name         string
		overrideHost string
		origin       string
	}{
		{name: "bad host", overrideHost: "attacker.example"},
		{name: "bad origin", origin: "http://attacker.example"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodGet, baseUrl+"/state", nil)
			if err != nil {
				t.Fatal(err)
			}
			if test.overrideHost != "" {
				request.Host = test.overrideHost
			}
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			resp, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("expected status %d, got %d", http.StatusForbidden, resp.StatusCode)
			}
		})
	}
	if backendRequests != 0 {
		t.Fatalf("expected no backend requests, got %d", backendRequests)
	}
}

func TestInvokeRemotePollsStoppedInstanceUntilRunning(t *testing.T) {
	captureBrowserOpen(t)
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	if err := saveRleState(rleState{
		EnvironmentName:    "code_rl",
		ProjectEndpoint:    "https://account.services.ai.azure.com/api/projects/project-1",
		EnvironmentId:      "env-1",
		EnvironmentVersion: "1.0.0",
	}); err != nil {
		t.Fatal(err)
	}

	oldPollInterval := remoteInstancePollInterval
	remoteInstancePollInterval = time.Millisecond
	defer func() { remoteInstancePollInterval = oldPollInterval }()

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
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/versions/1.0.0/instance_groups":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(
				`{"id":"group-1","environmentName":"code_rl",` +
					`"environmentVersion":"1.0.0","maxActiveInstances":1}`,
			))
		case r.Method == http.MethodPost &&
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/versions/1.0.0/instance_groups/group-1/instances":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"instanceId":"instance-1","instanceGroupId":"group-1","status":"Stopped"}`))
		case r.Method == http.MethodGet &&
			r.URL.Path == testFoundryProjectPath+
				"/rl_environments/code_rl/versions/1.0.0/instance_groups/group-1/instances/instance-1":
			getCount++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(
				`{"instanceId":"instance-1","instanceGroupId":"group-1","status":"Running","baseUrl":` +
					strconv.Quote(envServer.URL) + `}`,
			))
		case r.Method == http.MethodDelete &&
			r.URL.Path == testFoundryProjectPath+
				"/rl_environments/code_rl/versions/1.0.0/instance_groups/group-1/instances/instance-1":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete &&
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/versions/1.0.0/instance_groups/group-1":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected instance request: %s %s", r.Method, r.URL.Path)
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
		t.Fatalf("expected one instance poll, got %d", getCount)
	}
	if !strings.Contains(output.String(), "Environment code_rl version 1.0.0 ready") {
		t.Fatalf("expected environment ready output, got %s", output.String())
	}
	if strings.Contains(output.String(), envServer.URL) {
		t.Fatalf("expected instance data-plane URL to remain hidden, got %s", output.String())
	}
}

func TestWaitForRemoteInstanceRejectsDeletedInstance(t *testing.T) {
	_, err := waitForRemoteInstance(
		t.Context(),
		nil,
		"code_rl",
		nil,
		&instanceResource{Status: instanceStatusDeleted},
	)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T: %v", err, err)
	}
	if localErr.Code != "rle_instance_start_deleted" {
		t.Fatalf("expected deleted-instance code, got %q", localErr.Code)
	}
}

func TestInvokeRemoteFailsWhenInstanceFails(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	if err := saveRleState(rleState{
		EnvironmentName:    "code_rl",
		ProjectEndpoint:    "https://account.services.ai.azure.com/api/projects/project-1",
		EnvironmentId:      "env-1",
		EnvironmentVersion: "1.0.0",
	}); err != nil {
		t.Fatal(err)
	}

	instanceDeleted := false
	groupDeleted := false
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost &&
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/versions/1.0.0/instance_groups":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(
				`{"id":"group-1","environmentName":"code_rl",` +
					`"environmentVersion":"1.0.0","maxActiveInstances":1}`,
			))
		case r.Method == http.MethodPost &&
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/versions/1.0.0/instance_groups/group-1/instances":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(
				`{"instanceId":"instance-1","instanceGroupId":"group-1",` +
					`"status":"Failed","error":"image pull failed"}`,
			))
		case r.Method == http.MethodDelete &&
			r.URL.Path == testFoundryProjectPath+
				"/rl_environments/code_rl/versions/1.0.0/instance_groups/group-1/instances/instance-1":
			instanceDeleted = true
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete &&
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/versions/1.0.0/instance_groups/group-1":
			groupDeleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected instance request: %s %s", r.Method, r.URL.Path)
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
	if localErr.Code != "rle_instance_start_failed" {
		t.Fatalf("expected instance failed code, got %q", localErr.Code)
	}
	if !instanceDeleted || !groupDeleted {
		t.Fatal("expected failed instance and its group to be deleted")
	}
}

func TestRequireDeployedEnvironmentRejectsMissingEnvironmentId(t *testing.T) {
	err := requireDeployedEnvironment(rleState{
		ProjectEndpoint: "https://account.services.ai.azure.com/api/projects/project-1",
	})
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T", err)
	}
	if localErr.Code != "rle_environment_not_deployed" {
		t.Fatalf("expected not deployed code, got %q", localErr.Code)
	}
}

func TestRequireDeployedEnvironmentRejectsMissingEnvironmentVersion(t *testing.T) {
	err := requireDeployedEnvironment(rleState{
		EnvironmentName: "code_rl",
		ProjectEndpoint: "https://account.services.ai.azure.com/api/projects/project-1",
		EnvironmentId:   "env-1",
	})
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T", err)
	}
	if localErr.Code != "rle_environment_version_missing" {
		t.Fatalf("expected environment-version-missing code, got %q", localErr.Code)
	}
}

func TestLocalContainerNamesUseEnvironmentName(t *testing.T) {
	if name := localContainerName("code_rl"); name != "azd-rle-code-rl" {
		t.Fatalf("expected local container name, got %q", name)
	}
}

func TestEnsurePortAvailableRejectsBoundPort(t *testing.T) {
	// This test intentionally binds an ephemeral port on all interfaces to verify conflict detection.
	listener, err := net.Listen("tcp", ":0") //nolint:gosec
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
	if state.EnvironmentName != "my-env" {
		t.Fatalf("expected source-folder name, got %q", state.EnvironmentName)
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
	if saved != (rleState{EnvironmentName: "my-env"}) {
		t.Fatalf("expected saved state with only name, got %#v", saved)
	}
}

func TestInvokeRemoteRejectsMismatchedPinnedGroupVersionAndCleansUpRequestedRoute(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	if err := saveRleState(rleState{
		EnvironmentName:    "code_rl",
		ProjectEndpoint:    "https://account.services.ai.azure.com/api/projects/project-1",
		EnvironmentId:      "env-1",
		EnvironmentVersion: "1.0.0",
	}); err != nil {
		t.Fatal(err)
	}

	instanceCreateCount := 0
	groupDeleteCount := 0
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost &&
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/versions/1.0.0/instance_groups":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(
				`{"id":"group-1","environmentName":"code_rl",` +
					`"environmentVersion":"2.0.0","maxActiveInstances":1}`,
			))
		case r.Method == http.MethodPost &&
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/versions/2.0.0/instance_groups/group-1/instances":
			instanceCreateCount++
			t.Fatalf("unexpected instance creation against mismatched version route")
		case r.Method == http.MethodDelete &&
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/versions/1.0.0/instance_groups/group-1":
			groupDeleteCount++
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
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
	if localErr.Code != "rle_instance_group_version_mismatch" {
		t.Fatalf("expected version-mismatch code, got %q", localErr.Code)
	}
	if instanceCreateCount != 0 {
		t.Fatalf("expected no instance creation, got %d", instanceCreateCount)
	}
	if groupDeleteCount != 1 {
		t.Fatalf("expected cleanup to delete the group via the requested route, got %d", groupDeleteCount)
	}
}

func TestValidateInstanceGroupIdentityRejectsMismatchedEnvironment(t *testing.T) {
	err := validateInstanceGroupIdentity(
		rleState{EnvironmentName: "code_rl"},
		&instanceGroupResource{
			Id:                 "group-1",
			EnvironmentName:    "other_environment",
			EnvironmentVersion: "1.0.0",
		},
	)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T: %v", err, err)
	}
	if localErr.Code != "rle_instance_group_environment_mismatch" {
		t.Fatalf("expected environment-mismatch code, got %q", localErr.Code)
	}
}

func TestRemoteInvokeDoesNotRetryInstanceGroupConflicts(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	if err := saveRleState(rleState{
		EnvironmentName:    "code_rl",
		ProjectEndpoint:    "https://account.services.ai.azure.com/api/projects/project-1",
		EnvironmentId:      "env-1",
		EnvironmentVersion: "1.0.0",
	}); err != nil {
		t.Fatal(err)
	}

	createCount := 0
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost &&
			r.URL.Path == testFoundryProjectPath+"/rl_environments/code_rl/versions/1.0.0/instance_groups" {
			createCount++
			http.Error(w, `{"error":"quota unavailable"}`, http.StatusConflict)
			return
		}
		t.Fatalf("unexpected instance-group request: %s %s", r.Method, r.URL.Path)
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
		t.Fatal("expected instance-group conflict")
	}
	serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
	if !ok {
		t.Fatalf("expected ServiceError, got %T", err)
	}
	if createCount != 1 {
		t.Fatalf("expected one instance-group creation attempt, got %d", createCount)
	}
	if !strings.Contains(serviceErr.Message, "quota unavailable") {
		t.Fatalf("expected lease conflict details, got %q", serviceErr.Message)
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

func newAuthorizedPlaygroundClient(t *testing.T, playgroundUrl string) (*http.Client, string, string) {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	resp, err := client.Get(playgroundUrl) //nolint:gosec // Test-only local proxy URL.
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	baseUrl := playgroundBaseURL(t, playgroundUrl)
	return client, baseUrl, baseUrl
}

func playgroundBaseURL(t *testing.T, playgroundUrl string) string {
	t.Helper()
	parsed, err := url.Parse(playgroundUrl)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Scheme + "://" + parsed.Host
}

func useTestProjectEndpoint(t *testing.T, endpoint string) {
	t.Helper()
	target, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	oldCreateRleClient := createRleClient
	oldValidateSandboxURL := validateSandboxURL
	validateSandboxURL = func(string, string) error {
		return nil
	}
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
		validateSandboxURL = oldValidateSandboxURL
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
	if state.EnvironmentName != "my-env" {
		t.Fatalf("expected source-folder name, got %q", state.EnvironmentName)
	}
	if state.ProjectEndpoint != "https://account.services.ai.azure.com/api/projects/project-1" {
		t.Fatalf("expected saved project endpoint, got %q", state.ProjectEndpoint)
	}
}

func TestResolvePublishImageUsesTerminalAcrRegistryEnvironment(t *testing.T) {
	t.Setenv("AZURE_CONTAINER_REGISTRY_ENDPOINT", "example.azurecr.io")

	image, err := resolvePublishImage(
		rleState{
			EnvironmentName: "My Env",
			ProjectEndpoint: "https://account.services.ai.azure.com/api/projects/Project 1",
		},
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
		rleState{
			EnvironmentName: "my-env",
			ProjectEndpoint: "https://account.services.ai.azure.com/api/projects/project-1",
		},
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
			EnvironmentName: "my-env",
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
		rleState{EnvironmentName: defaultSourceName(tempDir)},
	)
	if image != "my-env:local" {
		t.Fatalf("expected source folder image, got %q", image)
	}
}
