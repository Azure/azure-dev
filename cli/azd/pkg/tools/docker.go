package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/executil"
)

func NewDocker(args DockerArgs) *Docker {
	if args.RunWithResultFn == nil {
		args.RunWithResultFn = executil.RunWithResult
	}

	return &Docker{
		runWithResultFn: args.RunWithResultFn,
	}
}

type DockerArgs struct {
	RunWithResultFn func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error)
}

type Docker struct {
	runWithResultFn func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error)
}

// Build runs a docker build for a given Dockerfile, forcing the amd64 platform. If successful, it
// returns the image id of the built image.
func (d *Docker) Build(ctx context.Context, cwd string, dockerFilePath string, platform string, buildContext string) (string, error) {
	if strings.TrimSpace(platform) == "" {
		platform = "amd64"
	}

	res, err := d.executeCommand(ctx, cwd, "build", "-q", "-f", dockerFilePath, "--platform", platform, buildContext)
	if err != nil {
		return "", fmt.Errorf("building image: %s: %w", res.String(), err)
	}

	return strings.TrimSpace(res.Stdout), nil
}

func (d *Docker) Tag(ctx context.Context, cwd string, imageName string, tag string) error {
	res, err := d.executeCommand(ctx, cwd, "tag", imageName, tag)
	if err != nil {
		return fmt.Errorf("tagging image: %s: %w", res.String(), err)
	}

	return nil
}

func (d *Docker) Push(ctx context.Context, cwd string, tag string) error {
	res, err := d.executeCommand(ctx, cwd, "push", tag)
	if err != nil {
		return fmt.Errorf("pushing image: %s: %w", res.String(), err)
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
	return d.runWithResultFn(ctx, executil.RunArgs{
		Cmd:         "docker",
		Args:        args,
		Cwd:         cwd,
		EnrichError: true,
	})
}
