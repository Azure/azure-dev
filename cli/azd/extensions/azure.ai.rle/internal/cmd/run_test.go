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
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"azure.ai.rle/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func TestLocalRunValidatesPortBeforeRuntimeAccess(t *testing.T) {
	for _, port := range []int{-1, 0, 65536} {
		t.Run(fmt.Sprint(port), func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			writeRunRleConfig(t, dir, project.RleHarnessManifest{Type: project.HarnessTypeGym})
			t.Setenv("AZD_CONTAINER_RUNTIME", filepath.Join(dir, "missing-runtime"))
			command := newRunCommand()
			command.SetArgs([]string{"--port", fmt.Sprint(port)})
			command.SetOut(io.Discard)
			command.SetErr(io.Discard)
			err := command.Execute()
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			if !ok || localErr.Code != "rle_invalid_local_port" {
				t.Fatalf("expected invalid port error before runtime access, got %v", err)
			}
		})
	}
}

func TestLocalRunPortDefaultsAndBounds(t *testing.T) {
	command := newRunCommand()
	if got := command.Flags().Lookup("port").DefValue; got != "8000" {
		t.Fatalf("expected port 8000 by default, got %s", got)
	}
	for _, port := range []int{1, defaultPort, 65535} {
		t.Run(fmt.Sprint(port), func(t *testing.T) {
			missingRuntime := filepath.Join(t.TempDir(), "missing-podman")
			t.Setenv("AZD_CONTAINER_RUNTIME", missingRuntime)
			_, err := ensureLocalContainerEndpointForName(command, &localRunFlags{port: port}, "test")
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			if !ok || localErr.Code != "rle_container_runtime_not_found" {
				t.Fatalf("expected valid port to reach runtime preflight, got %v", err)
			}
			if !strings.Contains(localErr.Message, "missing-podman") ||
				!strings.Contains(localErr.Suggestion, "AZD_CONTAINER_RUNTIME") {
				t.Fatalf("expected guidance for the selected runtime, got %#v", localErr)
			}
		})
	}
}

func TestLocalRunUsesSelectedRuntimeWithoutDocker(t *testing.T) {
	dir := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv("AZD_CONTAINER_RUNTIME", executable)
	t.Setenv("RLE_TEST_PUBLISH_CONTAINER_RUNTIME", "1")
	t.Setenv("RLE_TEST_PUBLISH_CONTAINER_FAIL", "build")
	capture := filepath.Join(dir, "container-calls.jsonl")
	t.Setenv("RLE_TEST_PUBLISH_CONTAINER_CAPTURE", capture)
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := newRunCommand()
	command.SetContext(t.Context())
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	_, err = ensureLocalContainerEndpointForName(command, &localRunFlags{port: defaultPort, source: dir}, "test")
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok || localErr.Code != "rle_local_docker_build_failed" {
		t.Fatalf("expected the selected runtime's deliberate build failure, got %v", err)
	}
	//nolint:gosec // Test-owned capture file written by the fake container runtime.
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	if err := json.Unmarshal(data, &args); err != nil {
		t.Fatal(err)
	}
	if len(args) == 0 || args[0] != "build" {
		t.Fatalf("expected a build through the selected executable, got %v", args)
	}
}

func TestLocalPortBindErrors(t *testing.T) {
	for _, test := range []struct {
		name  string
		cause error
		code  string
	}{
		{name: "occupied", cause: syscall.EADDRINUSE, code: "rle_local_port_in_use"},
		{name: "permission denied", cause: syscall.EACCES, code: "rle_local_port_bind_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("AZD_CONTAINER_RUNTIME", "podman")
			err := localPortBindError(9000, &net.OpError{Op: "listen", Net: "tcp", Err: test.cause})
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			if !ok || localErr.Code != test.code {
				t.Fatalf("expected %s, got %v", test.code, err)
			}
			if test.code == "rle_local_port_bind_failed" && !errors.Is(err, test.cause) {
				t.Fatalf("expected bind failure cause to be preserved, got %v", err)
			}
			if test.code == "rle_local_port_in_use" &&
				(!strings.Contains(localErr.Suggestion, "podman") || strings.Contains(localErr.Suggestion, "docker")) {
				t.Fatalf("expected recovery guidance for Podman, got %s", localErr.Suggestion)
			}
		})
	}
}

func TestEnsurePortAvailableReportsOccupiedPort(t *testing.T) {
	//nolint:gosec // Match the preflight's wildcard listener to test an actual bind conflict.
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	})
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("expected a TCP address, got %T", listener.Addr())
	}
	err = ensurePortAvailable(address.Port)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok || localErr.Code != "rle_local_port_in_use" {
		t.Fatalf("expected an occupied-port error, got %v", err)
	}
}

func writeRunRleConfig(t *testing.T, dir string, harness project.RleHarnessManifest) {
	t.Helper()

	schemaVersion := "1.0.0"
	manifest := project.RleManifest{
		Name:    "code_repair",
		Version: "1.0.0",
	}
	if err := project.WriteRleConfig(dir, project.RleConfig{
		SchemaVersion: &schemaVersion,
		Rle:           manifest,
		Harness:       harness,
	}); err != nil {
		t.Fatal(err)
	}
}

func byohHarness(protocol project.HarnessProtocol) project.RleHarnessManifest {
	endpoint := "https://agent.example.test"
	return project.RleHarnessManifest{Type: project.HarnessTypeBYOH, Endpoint: &endpoint, Protocol: protocol}
}

func TestLocalRunAcceptsGymOpenEnv(t *testing.T) {
	dir := t.TempDir()
	writeRunRleConfig(t, dir, project.RleHarnessManifest{Type: project.HarnessTypeGym})

	config, err := loadLocalRunConfig(&localRunFlags{source: dir})
	if err != nil {
		t.Fatalf("expected a gym manifest to be runnable, got %v", err)
	}
	if config.Rle.Name != "code_repair" {
		t.Fatalf("expected the manifest to be returned, got %#v", config.Rle)
	}
}

// A harness container serves no OpenEnv WebSocket endpoint, so run has to fail
// before it builds an image rather than at the handshake.
func TestLocalRunRejectsByohHarnesses(t *testing.T) {
	for _, protocol := range []project.HarnessProtocol{
		project.HarnessProtocolRleInvocation,
		project.HarnessProtocolResponses,
	} {
		t.Run(string(protocol), func(t *testing.T) {
			dir := t.TempDir()
			writeRunRleConfig(t, dir, byohHarness(protocol))

			_, err := loadLocalRunConfig(&localRunFlags{source: dir})
			if err == nil {
				t.Fatalf("expected byoh (%s) to be rejected by azd ai rle run", protocol)
			}

			var localErr *azdext.LocalError
			if !errors.As(err, &localErr) {
				t.Fatalf("expected a LocalError, got %v", err)
			}
			if localErr.Code != "rle_run_unsupported_type" {
				t.Fatalf("expected code rle_run_unsupported_type, got %q", localErr.Code)
			}
			if localErr.Category != azdext.LocalErrorCategoryUser {
				t.Fatalf("expected a user-category error, got %q", localErr.Category)
			}
			// The message has to name what was declared, or the user cannot
			// tell which of their manifests the CLI is objecting to.
			if !strings.Contains(localErr.Message, "declares byoh") {
				t.Fatalf("expected the declared type in the message, got %q", localErr.Message)
			}
			// And it has to point at the loop that does work for a harness.
			for _, want := range []string{"azd ai rle publish", "azd ai rle rollout"} {
				if !strings.Contains(localErr.Suggestion, want) {
					t.Fatalf("expected the suggestion to mention %q, got %q", want, localErr.Suggestion)
				}
			}
		})
	}
}

// The watch loop reloads the manifest through loadLocalRunConfig on every
// restart, so the gate has to hold there too rather than only on the first
// load.
func TestLocalRunGateAppliesToRestartPath(t *testing.T) {
	dir := t.TempDir()
	writeRunRleConfig(t, dir, byohHarness(project.HarnessProtocolRleInvocation))

	restartFlags := localRunFlags{source: dir, restart: true}
	_, err := loadLocalRunConfig(&restartFlags)
	if err == nil {
		t.Fatal("expected the restart path to reject a harness manifest")
	}

	var localErr *azdext.LocalError
	if !errors.As(err, &localErr) || localErr.Code != "rle_run_unsupported_type" {
		t.Fatalf("expected the run type gate to reject it, got %v", err)
	}
}

func TestEnsureLocalRunSupportedNamesDeclaredType(t *testing.T) {
	err := ensureLocalRunSupported(project.RleConfig{
		Harness: project.RleHarnessManifest{Type: project.HarnessTypeBYOH},
	})
	if err == nil {
		t.Fatal("expected a byoh manifest to be rejected")
	}

	var localErr *azdext.LocalError
	if !errors.As(err, &localErr) {
		t.Fatalf("expected a LocalError, got %v", err)
	}
	if !strings.Contains(localErr.Message, "declares byoh") {
		t.Fatalf("expected the declared type in the message, got %q", localErr.Message)
	}
}
