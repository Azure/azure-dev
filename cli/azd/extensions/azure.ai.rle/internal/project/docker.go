// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

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
			Suggestion: "Run az acr login --name <namespace>, then retry.",
		}
	}
	return nil
}

func ContainerStatus(ctx context.Context, container string) (running bool, exists bool) {
	//nolint:gosec // Fixed docker inspect command; container is a generated local name.
	process := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Running}}", container)
	process.Env = os.Environ()
	output, err := process.Output()
	if err != nil {
		return false, false
	}
	return strings.TrimSpace(string(output)) == "true", true
}

func RunDocker(ctx context.Context, stdout io.Writer, stderr io.Writer, args ...string) error {
	//nolint:gosec // Fixed docker command shapes with selected names/tags.
	process := exec.CommandContext(ctx, "docker", args...)
	process.Stdout = stdout
	process.Stderr = stderr
	process.Env = os.Environ()
	return process.Run()
}
