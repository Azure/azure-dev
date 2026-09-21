// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestContainerCommand(t *testing.T) {
	for _, tt := range []struct {
		name    string
		runtime string
		want    string
		unset   bool
	}{
		{name: "unset", want: "docker", unset: true},
		{name: "empty", want: "docker"},
		{name: "docker", runtime: "docker", want: "docker"},
		{name: "podman", runtime: "podman", want: "podman"},
		{name: "path with spaces", runtime: `C:\Container Tools\podman.exe`, want: `C:\Container Tools\podman.exe`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AZD_CONTAINER_RUNTIME", tt.runtime)
			if tt.unset {
				if err := os.Unsetenv("AZD_CONTAINER_RUNTIME"); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("RLE_TEST_CONTAINER_ENV", "inherited value")
			args := []string{"build", "-t", "example:local", "-f", `C:\RLE Tests\Dockerfile`, `C:\RLE Tests`}
			command := containerCommand(t.Context(), args...)
			if command.Args[0] != tt.want {
				t.Fatalf("runtime = %q, want %q", command.Args[0], tt.want)
			}
			if !slices.Equal(command.Args[1:], args) {
				t.Fatalf("arguments = %v, want %v", command.Args[1:], args)
			}
			if !slices.Contains(command.Env, "RLE_TEST_CONTAINER_ENV=inherited value") {
				t.Fatal("container command did not inherit the environment")
			}
		})
	}
}

func TestRunDockerUsesSelectedExecutable(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AZD_CONTAINER_RUNTIME", executable)
	var stdout, stderr bytes.Buffer
	if err := RunDocker(t.Context(), &stdout, &stderr, "-test.run=^$"); err != nil {
		t.Fatalf("selected executable failed: %v; stderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "PASS") {
		t.Fatalf("expected output from the selected executable, got %q", stdout.String())
	}
}

func TestRunDockerMissingRuntime(t *testing.T) {
	missingRuntime := filepath.Join(t.TempDir(), "rle-nonexistent-container-runtime")
	t.Setenv("AZD_CONTAINER_RUNTIME", missingRuntime)
	err := RunDocker(t.Context(), io.Discard, io.Discard, "version")
	if err == nil || !strings.Contains(err.Error(), filepath.Base(missingRuntime)) {
		t.Fatalf("expected an error identifying the missing runtime %q, got %v", missingRuntime, err)
	}
}
