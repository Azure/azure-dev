package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/executil"
)

func NewDocker() *Docker {
	return &Docker{}
}

type Docker struct {
}

// Build runs a docker build for a given Dockerfile, forcing the amd64 platform. If successful, it
// returns the image id of the built image.
func (d *Docker) Build(ctx context.Context, dockerFilePath string, buildContext string) (string, error) {
	res, err := d.executeCommand(ctx, buildContext, "build", "-q", "-f", dockerFilePath, "--platform", "amd64", ".")
	if err != nil {
		return "", fmt.Errorf("building image: %s: %w", res.String(), err)
	}

	return strings.TrimSpace(res.Stdout), nil
}

func (d *Docker) Tag(ctx context.Context, imageName string, tag string) error {
	res, err := d.executeCommand(ctx, ".", "tag", imageName, tag)
	if err != nil {
		return fmt.Errorf("tagging image: %s: %w", res.String(), err)
	}

	return nil
}

func (d *Docker) Push(ctx context.Context, tag string) error {
	res, err := d.executeCommand(ctx, ".", "push", tag)
	if err != nil {
		return fmt.Errorf("tagging image: %s: %w", res.String(), err)
	}

	return nil
}

func (d *Docker) CheckInstalled(_ context.Context) (bool, error) {
	return toolInPath("docker")
}

func (d *Docker) InstallUrl() string {
	return "https://aka.ms/azure-dev/docker-install"
}

func (d *Docker) Name() string {
	return "Docker"
}

func (d *Docker) executeCommand(ctx context.Context, cwd string, args ...string) (executil.RunResult, error) {
	return executil.RunWithResult(ctx, executil.RunArgs{
		Cmd:  "docker",
		Args: args,
		Cwd:  cwd,
	})
}
