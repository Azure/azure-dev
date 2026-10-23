// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
)

func TestBuildAndPushImageRemote(t *testing.T) {
	for _, tc := range []struct {
		name       string
		image      string
		dockerfile string
		buildErr   error
	}{
		{name: "default Dockerfile"},
		{name: "case-insensitive registry", image: "EXAMPLE.AZURECR.IO/project/harbor:task-hash"},
		{name: "nested Dockerfile", dockerfile: "server/custom Dockerfile"},
		{name: "failed build", buildErr: errors.New("remote build failed")},
		{name: "missing Azure CLI", buildErr: &exec.Error{Name: "az", Err: exec.ErrNotFound}},
		{name: "interrupted client", buildErr: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "source with spaces")
			dockerfile := tc.dockerfile
			if dockerfile == "" {
				dockerfile = "Dockerfile"
			}
			if err := os.MkdirAll(filepath.Dir(filepath.Join(source, dockerfile)), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source, dockerfile), []byte("FROM scratch\n"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("AZD_CONTAINER_RUNTIME", filepath.Join(t.TempDir(), "missing-local-runtime"))
			oldRun := runAcrBuild
			t.Cleanup(func() { runAcrBuild = oldRun })
			var captured azdexec.RunArgs
			calls := 0
			runAcrBuild = func(ctx context.Context, args azdexec.RunArgs) (azdexec.RunResult, error) {
				calls++
				captured = args
				_, _ = io.WriteString(args.StdOut, "remote build output")
				_, _ = io.WriteString(args.Stderr, "remote build diagnostics")
				return azdexec.RunResult{}, tc.buildErr
			}
			var stdout, stderr bytes.Buffer
			image := tc.image
			if image == "" {
				image = "example.azurecr.io/project/harbor:task-hash"
			}
			err := BuildAndPushImage(t.Context(), &stdout, &stderr,
				image,
				BuildOptions{Source: source, Dockerfile: tc.dockerfile}, true)
			if tc.buildErr != nil {
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				if !errors.Is(err, tc.buildErr) || !ok || localErr.Code != "rle_remote_image_build_failed" {
					t.Fatalf("expected actionable wrapped remote error, got %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			wantArgs := []string{"acr", "build", "--registry", "example",
				"--image", "project/harbor:task-hash", "--file", filepath.FromSlash(dockerfile),
				"--platform", "linux/amd64", "."}
			if calls != 1 || captured.Cmd != "az" || !slices.Equal(captured.Args, wantArgs) ||
				captured.Cwd != source || captured.UseShell || captured.Interactive {
				t.Fatalf("unexpected remote invocation: calls=%d args=%+v", calls, captured)
			}
			if stdout.String() != "remote build output" || !strings.Contains(stderr.String(), "remote build diagnostics") {
				t.Fatalf("remote output was not forwarded: stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestBuildAndPushImageRemoteRejectsInvalidInputs(t *testing.T) {
	oldRun := runAcrBuild
	t.Cleanup(func() { runAcrBuild = oldRun })
	runAcrBuild = func(context.Context, azdexec.RunArgs) (azdexec.RunResult, error) {
		t.Fatal("invalid input must not submit a remote build")
		return azdexec.RunResult{}, nil
	}
	for _, tc := range []struct {
		image string
		opts  BuildOptions
	}{
		{image: "docker.io/example:tag"},
		{image: "example.azurecr.io/"},
		{image: "example.azurecr.io/repo:tag", opts: BuildOptions{Source: t.TempDir()}},
		{image: "example.azurecr.io/repo:tag", opts: BuildOptions{Dockerfile: "../Dockerfile"}},
	} {
		if err := BuildAndPushImage(t.Context(), io.Discard, io.Discard, tc.image, tc.opts, true); err == nil {
			t.Fatalf("expected validation failure for %+v", tc)
		}
	}
}

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
			if got := ContainerRuntime(); got != tt.want {
				t.Fatalf("selected runtime = %q, want %q", got, tt.want)
			}
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
