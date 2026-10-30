// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
)

var runAcrBuild = azdexec.NewCommandRunner(nil).Run

// BuildAndPushImage builds locally by default, or builds and pushes remotely with ACR Tasks.
func BuildAndPushImage(
	ctx context.Context,
	stdout io.Writer,
	stderr io.Writer,
	image string,
	opts BuildOptions,
	remote bool,
) error {
	if !remote {
		if err := BuildRuntimeImage(ctx, stdout, stderr, image, opts); err != nil {
			return err
		}
		return PushImage(ctx, stdout, stderr, image)
	}
	host, tag, _ := strings.Cut(image, "/")
	registry := strings.TrimSuffix(strings.ToLower(host), ".azurecr.io")
	if !IsAcrImageReference(image) || registry == "" || tag == "" {
		return &azdext.LocalError{
			Message:    fmt.Sprintf("ACR remote builds require an ACR image reference, got %q.", image),
			Code:       "rle_acr_image_required",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Set AZURE_CONTAINER_REGISTRY_ENDPOINT=<registry>.azurecr.io.",
		}
	}
	source, dockerfile, cleanup, err := PrepareDockerBuild(opts)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}
	dockerfile, err = filepath.Rel(source, dockerfile)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stderr, "Building and pushing image %s remotely with ACR Tasks ...\n", image); err != nil {
		return err
	}
	_, err = runAcrBuild(ctx, azdexec.NewRunArgs("az", "acr", "build",
		"--registry", registry, "--image", tag, "--file", dockerfile, "--platform", "linux/amd64", ".").
		WithCwd(source).WithStdOut(stdout).WithStdErr(stderr))
	if err != nil {
		return fmt.Errorf("%w: %w", &azdext.LocalError{
			Message:  fmt.Sprintf("ACR remote build or push failed for image %q.", image),
			Code:     "rle_remote_image_build_failed",
			Category: azdext.LocalErrorCategoryUser,
			Suggestion: "Check Azure CLI installation, sign-in, subscription, ACR Tasks permissions, and build logs. " +
				"The remote run may still be active; inspect 'az acr task list-runs --registry " + registry +
				"' before retrying. There is no fallback to a local build.",
		}, err)
	}
	return nil
}

func BuildRuntimeImage(
	ctx context.Context,
	stdout io.Writer,
	stderr io.Writer,
	image string,
	opts BuildOptions,
) error {
	source, dockerfile, cleanup, err := PrepareDockerBuild(opts)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}
	if _, err := fmt.Fprintf(
		stderr,
		"Building local runtime image %s from %s ...\n",
		image,
		dockerfile,
	); err != nil {
		return err
	}
	if err := RunDocker(ctx, stdout, stderr, "build", "-t", image, "-f", dockerfile, source); err != nil {
		return &azdext.LocalError{
			Message:    fmt.Sprintf("Failed to build Docker image %q: %v", image, err),
			Code:       "rle_local_docker_build_failed",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Fix the Dockerfile or generated environment code, then retry.",
		}
	}
	return nil
}

func PushImage(ctx context.Context, stdout io.Writer, stderr io.Writer, image string) error {
	if _, err := fmt.Fprintf(stderr, "Pushing image %s ...\n", image); err != nil {
		return err
	}
	if err := RunDocker(ctx, stdout, stderr, "push", image); err != nil {
		return &azdext.LocalError{
			Message:    fmt.Sprintf("Failed to push Docker image %q: %v", image, err),
			Code:       "rle_docker_push_failed",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Run az acr login --name <registry>, then retry.",
		}
	}
	return nil
}

func ContainerStatus(ctx context.Context, container string) (running bool, exists bool) {
	process := containerCommand(ctx, "inspect", "-f", "{{.State.Running}}", container)
	output, err := process.Output()
	if err != nil {
		return false, false
	}
	return strings.TrimSpace(string(output)) == "true", true
}

func RunDocker(ctx context.Context, stdout io.Writer, stderr io.Writer, args ...string) error {
	process := containerCommand(ctx, args...)
	process.Stdout = stdout
	process.Stderr = stderr
	return process.Run()
}

// ContainerRuntime returns the selected container executable, defaulting to Docker.
func ContainerRuntime() string {
	runtime := os.Getenv("AZD_CONTAINER_RUNTIME")
	if runtime == "" {
		runtime = "docker"
	}
	return runtime
}

func containerCommand(ctx context.Context, args ...string) *exec.Cmd {
	//nolint:gosec // Explicit local runtime override; arguments are passed without a shell.
	process := exec.CommandContext(ctx, ContainerRuntime(), args...)
	process.Env = os.Environ()
	return process
}
