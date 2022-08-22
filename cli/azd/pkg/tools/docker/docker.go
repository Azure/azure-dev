package docker

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/executil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
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

// Runs a Docker build for a given Dockerfile. If the platform is not specified (empty), it defaults to amd64. If the build is successful, the function
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

func (d *Docker) versionInfo() tools.VersionInfo {
	return tools.VersionInfo{
		MinimumVersion: semver.Version{
			Major: 17,
			Minor: 9,
			Patch: 0},
		UpdateCommand: "Visit https://docs.docker.com/engine/release-notes/ to upgrade",
	}
}

func (d *Docker) extractDockerVersionSemVer(cliOutput string) (semver.Version, error) {
	ver := regexp.MustCompile(`\d+\.\d+\.\d+`).FindString(cliOutput)

	// Skip leading zeroes to allow inexact parsing for version formats that are not truly SemVer compliant.
	// Example: docker has versions like 17.09.0 (non semver) instead of 17.9.0 (semver)
	versionSplit := strings.Split(ver, ".")
	for key, val := range versionSplit {
		verInt, err := strconv.Atoi(val)
		if err != nil {
			return semver.Version{}, err
		}
		versionSplit[key] = strconv.Itoa(verInt)
	}

	semver, err := semver.Parse(strings.Join(versionSplit, "."))
	if err != nil {
		return semver, err
	}
	return semver, nil

}
func (d *Docker) CheckInstalled(ctx context.Context) (bool, error) {
	found, err := tools.ToolInPath("docker")
	if !found {
		return false, err
	}
	dockerRes, err := tools.ExecuteCommand(ctx, "docker", "--version")
	if err != nil {
		return false, fmt.Errorf("checking %s version: %w", d.Name(), err)
	}
	dockerSemver, err := d.extractDockerVersionSemVer(dockerRes)
	if err != nil {
		return false, fmt.Errorf("converting to semver version fails: %w", err)
	}
	updateDetail := d.versionInfo()
	if dockerSemver.LT(updateDetail.MinimumVersion) {
		return false, &tools.ErrSemver{ToolName: d.Name(), VersionInfo: updateDetail}
	}
	return true, nil
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
