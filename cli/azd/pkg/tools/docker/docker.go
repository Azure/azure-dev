package docker

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
)

func NewDocker(commandRunner exec.CommandRunner) *Docker {
	return &Docker{
		commandRunner: commandRunner,
	}
}

type Docker struct {
	commandRunner exec.CommandRunner
}

func (d *Docker) Login(ctx context.Context, loginServer string, username string, password string) error {
	_, err := d.executeCommand(ctx, ".", "login",
		"--username", username,
		"--password", password,
		loginServer)

	if err != nil {
		return fmt.Errorf("failed logging into docker: %w", err)
	}

	return nil
}

// Runs a Docker build for a given Dockerfile. If the platform is not specified (empty),
// it defaults to amd64. If the build
// is successful, the function
// returns the image id of the built image.
func (d *Docker) Build(
	ctx context.Context,
	cwd string,
	dockerFilePath string,
	platform string,
	buildContext string,
) (string, error) {
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

// dockerVersionRegexp is a regular expression which matches the text printed by "docker --version"
// and captures the version and build components.
var dockerVersionStringRegexp = regexp.MustCompile(`Docker version ([^,]*), build ([a-f0-9]*)`)

// dockerVersionReleaseBuildRegexp is a regular expression which matches the three part version number
// from a docker version from an official release. The major and minor components are captured.
var dockerVersionReleaseBuildRegexp = regexp.MustCompile(`^(\d+).(\d+).\d+`)

// dockerVersionMasterBuildRegexp is a regular expression which matches the three part version number
// from a docker version from a release from master. Each version component is captured independently.
var dockerVersionMasterBuildRegexp = regexp.MustCompile(`^master-dockerproject-(\d+)-(\d+)-(\d+)`)

// isSupportedDockerVersion returns true if the version string appears to be for a docker version
// of 17.09 or later and false if it does not.
func isSupportedDockerVersion(cliOutput string) (bool, error) {
	log.Printf("determining version from docker --version string: %s", cliOutput)

	matches := dockerVersionStringRegexp.FindStringSubmatch(cliOutput)

	// (3 matches, the entire string, and the two captures)
	if len(matches) != 3 {
		return false, fmt.Errorf("could not extract version component from docker version string")
	}

	version := matches[1]
	build := matches[2]

	log.Printf("extracted docker version: %s, build: %s from version string", version, build)

	// For official release builds, the version number looks something like:
	//
	// 17.09.0-ce or 20.10.17+azure-1
	//
	// Note this is not a semver (the leading zero in the second component of the 17.09 string is not allowed per semver)
	// so we need to take this apart ourselves.
	if releaseVersionMatches := dockerVersionReleaseBuildRegexp.FindStringSubmatch(version); releaseVersionMatches != nil {
		major, err := strconv.Atoi(releaseVersionMatches[1])
		if err != nil {
			return false, fmt.Errorf(
				"failed to convert major version component %s to an integer: %w",
				releaseVersionMatches[1],
				err,
			)
		}

		minor, err := strconv.Atoi(releaseVersionMatches[2])
		if err != nil {
			return false, fmt.Errorf(
				"failed to convert minor version component %s to an integer: %w",
				releaseVersionMatches[2],
				err,
			)
		}

		return (major > 17 || (major == 17 && minor >= 9)), nil
	}

	// For builds which come out of master, we'll assume any build from 2018 or later will work
	// (since we support 17.09 which was released in September of 2017)
	if masterVersionMatches := dockerVersionMasterBuildRegexp.FindStringSubmatch(version); masterVersionMatches != nil {
		year, err := strconv.Atoi(masterVersionMatches[1])
		if err != nil {
			return false, fmt.Errorf(
				"failed to convert major version component %s to an integer: %w",
				masterVersionMatches[1],
				err,
			)
		}

		return year >= 2018, nil
	}

	// If we reach this point, we don't understand how to validate the version based on its scheme.
	return false, fmt.Errorf("could not determine version from docker version string: %s", version)
}

func (d *Docker) CheckInstalled(ctx context.Context) (bool, error) {
	found, err := tools.ToolInPath("docker")
	if !found {
		return false, err
	}
	dockerRes, err := tools.ExecuteCommand(ctx, d.commandRunner, "docker", "--version")
	if err != nil {
		return false, fmt.Errorf("checking %s version: %w", d.Name(), err)
	}
	supported, err := isSupportedDockerVersion(dockerRes)
	if err != nil {
		return false, err
	}
	if !supported {
		return false, &tools.ErrSemver{ToolName: d.Name(), VersionInfo: d.versionInfo()}
	}
	return true, nil
}

func (d *Docker) InstallUrl() string {
	return "https://aka.ms/azure-dev/docker-install"
}

func (d *Docker) Name() string {
	return "Docker"
}

func (d *Docker) executeCommand(ctx context.Context, cwd string, args ...string) (exec.RunResult, error) {
	runArgs := exec.NewRunArgs("docker", args...).
		WithCwd(cwd).
		WithEnrichError(true)

	return d.commandRunner.Run(ctx, runArgs)
}
