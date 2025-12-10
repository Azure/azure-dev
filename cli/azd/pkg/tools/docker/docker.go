// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package docker

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
)

const DefaultPlatform string = "linux/amd64"

var _ tools.ExternalTool = (*Cli)(nil)

func NewCli(commandRunner exec.CommandRunner) *Cli {
	return &Cli{
		commandRunner:   commandRunner,
		containerEngine: "",
	}
}

type Cli struct {
	commandRunner   exec.CommandRunner
	containerEngine string // "docker" or "podman", detected during CheckInstalled
}

// getContainerEngine returns the container engine command to use ("docker" or "podman").
// CheckInstalled() should be called first to detect and set the container engine.
// If not set, defaults to "docker" for backward compatibility.
func (d *Cli) getContainerEngine() string {
	if d.containerEngine == "" {
		// Default to "docker" for backward compatibility with existing code
		// that may not call CheckInstalled() first
		return "docker"
	}
	return d.containerEngine
}

func (d *Cli) Login(ctx context.Context, loginServer string, username string, password string) error {
	runArgs := exec.NewRunArgs(
		d.getContainerEngine(), "login",
		"--username", username,
		"--password-stdin",
		loginServer,
	).WithStdIn(strings.NewReader(password))

	_, err := d.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("failed logging into %s: %w", d.Name(), err)
	}

	return nil
}

// Runs a Docker build for a given Dockerfile, writing the output of docker build to [stdOut] when it is
// not nil. If the platform is not specified (empty) it defaults to amd64. If the build is successful,
// the function returns the image id of the built image.
func (d *Cli) Build(
	ctx context.Context,
	cwd string,
	dockerFilePath string,
	platform string,
	target string,
	buildContext string,
	tagName string,
	buildArgs []string,
	buildSecrets []string,
	buildEnv []string,
	buildProgress io.Writer,
) (string, error) {
	if strings.TrimSpace(platform) == "" {
		platform = DefaultPlatform
	}

	tmpFolder, err := os.MkdirTemp(os.TempDir(), "azd-docker-build")
	defer func() {
		// fail to remove tmp files is not so bad as the OS will delete it
		// eventually
		_ = os.RemoveAll(tmpFolder)
	}()

	if err != nil {
		return "", fmt.Errorf("building image: %w", err)
	}
	imgIdFile := filepath.Join(tmpFolder, "imgId")

	args := []string{
		"build",
		"-f", dockerFilePath,
		"--platform", platform,
	}

	if target != "" {
		args = append(args, "--target", target)
	}

	if tagName != "" {
		args = append(args, "-t", tagName)
	}

	for _, arg := range buildArgs {
		args = append(args, "--build-arg", arg)
	}

	for _, arg := range buildSecrets {
		args = append(args, "--secret", arg)
	}
	args = append(args, buildContext)

	// create a file with the docker img id
	args = append(args, "--iidfile", imgIdFile)

	// Build and produce output
	runArgs := exec.NewRunArgs(d.getContainerEngine(), args...).WithCwd(cwd).WithEnv(buildEnv)

	if buildProgress != nil {
		// setting stderr and stdout both, as it's been noticed
		// that docker log goes to stderr on macOS, but stdout on Ubuntu.
		runArgs = runArgs.WithStdOut(buildProgress).WithStdErr(buildProgress)
	}

	_, err = d.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return "", fmt.Errorf("building image: %w", err)
	}

	imgId, err := os.ReadFile(imgIdFile)
	if err != nil {
		return "", fmt.Errorf("building image: %w", err)
	}
	return strings.TrimSpace(string(imgId)), nil
}

func (d *Cli) Tag(ctx context.Context, cwd string, imageName string, tag string) error {
	_, err := d.executeCommand(ctx, cwd, "tag", imageName, tag)
	if err != nil {
		return fmt.Errorf("tagging image: %w", err)
	}

	return nil
}

func (d *Cli) Push(ctx context.Context, cwd string, tag string) error {
	_, err := d.executeCommand(ctx, cwd, "push", tag)
	if err != nil {
		return fmt.Errorf("pushing image: %w", err)
	}

	return nil
}

func (d *Cli) Pull(ctx context.Context, imageName string) error {
	_, err := d.executeCommand(ctx, "", "pull", imageName)
	if err != nil {
		return fmt.Errorf("pulling image: %w", err)
	}

	return nil
}

func (d *Cli) Inspect(ctx context.Context, imageName string, format string) (string, error) {
	out, err := d.executeCommand(ctx, "", "image", "inspect", "--format", format, imageName)
	if err != nil {
		return "", fmt.Errorf("inspecting image: %w", err)
	}

	return out.Stdout, nil
}

// Remove deletes a local Docker image by name or ID
func (d *Cli) Remove(ctx context.Context, imageName string) error {
	_, err := d.executeCommand(ctx, "", "rmi", imageName)
	if err != nil {
		return fmt.Errorf("removing image %s: %w", imageName, err)
	}

	return nil
}

func (d *Cli) versionInfo() tools.VersionInfo {
	return tools.VersionInfo{
		MinimumVersion: semver.Version{
			Major: 17,
			Minor: 9,
			Patch: 0},
		UpdateCommand: "Visit https://docs.docker.com/engine/release-notes/ or " +
			"https://podman.io/getting-started/installation to upgrade",
	}
}

func (d *Cli) podmanVersionInfo() tools.VersionInfo {
	return tools.VersionInfo{
		MinimumVersion: semver.Version{
			Major: 3,
			Minor: 0,
			Patch: 0},
		UpdateCommand: "Visit https://podman.io/getting-started/installation to upgrade",
	}
}

// dockerVersionRegexp is a regular expression which matches the text printed by "docker --version"
// and captures the version and build components.
var dockerVersionStringRegexp = regexp.MustCompile(`Docker version ([^,]*), build ([a-f0-9]*)`)

// podmanVersionStringRegexp is a regular expression which matches the text printed by "podman --version"
// and captures the version component.
var podmanVersionStringRegexp = regexp.MustCompile(`podman version (\S+)`)

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

// isSupportedPodmanVersion returns true if the version string appears to be for a podman version
// of 3.0 or later (podman 3.0 was released in 2021 with stable docker compatibility)
func isSupportedPodmanVersion(cliOutput string) (bool, error) {
	log.Printf("determining version from podman --version string: %s", cliOutput)

	matches := podmanVersionStringRegexp.FindStringSubmatch(cliOutput)

	// (2 matches, the entire string, and the version capture)
	if len(matches) != 2 {
		return false, fmt.Errorf("could not extract version component from podman version string")
	}

	versionStr := matches[1]
	log.Printf("extracted podman version: %s from version string", versionStr)

	// Podman uses semantic versioning, so we can parse it directly
	version, err := semver.Parse(versionStr)
	if err != nil {
		return false, fmt.Errorf("failed to parse podman version %s: %w", versionStr, err)
	}

	// Require podman 3.0 or later for stable docker compatibility
	minVersion := semver.Version{Major: 3, Minor: 0, Patch: 0}
	return version.GTE(minVersion), nil
}
func (d *Cli) CheckInstalled(ctx context.Context) error {
	// Check for environment variable override first
	containerRuntime := os.Getenv("AZD_CONTAINER_RUNTIME")

	if containerRuntime != "" {
		// Validate the specified runtime
		if containerRuntime != "docker" && containerRuntime != "podman" {
			return fmt.Errorf(
				"unsupported container runtime '%s' specified in AZD_CONTAINER_RUNTIME. "+
					"Supported values: docker, podman",
				containerRuntime)
		}
		d.containerEngine = containerRuntime
	} else {
		// Auto-select: try docker first, then fall back to podman
		if d.commandRunner.ToolInPath("docker") == nil {
			d.containerEngine = "docker"
		} else if d.commandRunner.ToolInPath("podman") == nil {
			d.containerEngine = "podman"
		} else {
			// Neither tool is installed
			return fmt.Errorf(
				"neither docker nor podman is installed. " +
					"Please install Docker: https://aka.ms/azure-dev/docker-install " +
					"or Podman: https://aka.ms/azure-dev/podman-install")
		}
	}

	// Now validate the selected engine (version check and daemon/service running)
	return d.validateContainerEngine(ctx)
}

// validateContainerEngine validates that the selected container engine (docker or podman) meets version
// and readiness requirements.
// The engine must have been selected first via CheckInstalled (stored in d.containerEngine).
func (d *Cli) validateContainerEngine(ctx context.Context) error {
	engineName := d.getContainerEngine()

	// Check version
	versionOutput, err := tools.ExecuteCommand(ctx, d.commandRunner, engineName, "--version")
	if err != nil {
		return fmt.Errorf("checking %s version: %w", engineName, err)
	}
	log.Printf("%s version: %s", engineName, versionOutput)

	var supported bool
	var versionInfo tools.VersionInfo
	if engineName == "docker" {
		supported, err = isSupportedDockerVersion(versionOutput)
		versionInfo = d.versionInfo()
	} else if engineName == "podman" {
		supported, err = isSupportedPodmanVersion(versionOutput)
		versionInfo = d.podmanVersionInfo()
	} else {
		return fmt.Errorf("unknown container engine: %s", engineName)
	}

	if err != nil {
		return err
	}
	if !supported {
		return &tools.ErrSemver{ToolName: d.Name(), VersionInfo: versionInfo}
	}

	// Check if daemon/service is running
	if _, err := tools.ExecuteCommand(ctx, d.commandRunner, engineName, "ps"); err != nil {
		return fmt.Errorf("the %s service is not running, please start it: %w", engineName, err)
	}

	return nil
}

func (d *Cli) InstallUrl() string {
	if d.containerEngine == "podman" {
		return "https://aka.ms/azure-dev/podman-install"
	}
	return "https://aka.ms/azure-dev/docker-install"
}

func (d *Cli) Name() string {
	if d.containerEngine == "podman" {
		return "Podman"
	}
	return "Docker"
}

// IsContainerdEnabled checks if Docker is using containerd as the image store
func (d *Cli) IsContainerdEnabled(ctx context.Context) (bool, error) {
	// Containerd image store is only applicable to Docker, not Podman
	if d.getContainerEngine() == "podman" {
		return false, nil
	}

	result, err := d.executeCommand(ctx, "", "system", "info", "--format", "{{.DriverStatus}}")
	if err != nil {
		return false, fmt.Errorf("checking docker driver status: %w", err)
	}

	driverStatus := strings.TrimSpace(result.Stdout)

	// Check for containerd snapshotter which indicates containerd image store is enabled
	return strings.Contains(driverStatus, "io.containerd.snapshotter.v1"), nil
}

func (d *Cli) executeCommand(ctx context.Context, cwd string, args ...string) (exec.RunResult, error) {
	runArgs := exec.NewRunArgs(d.getContainerEngine(), args...).
		WithCwd(cwd)

	return d.commandRunner.Run(ctx, runArgs)
}

// SplitDockerImage splits the image into the name and tag.
// If the image does not have a tag or is invalid, the full string is returned as name, and tag will be empty.
func SplitDockerImage(fullImg string) (name string, tag string) {
	split := -1
	// the colon separator can appear in two places:
	// 1. between the image and the tag, image:tag
	// 2. between the host and the port, in which case, it would be host:port/image:tag to be valid.
	for i, r := range fullImg {
		switch r {
		case ':':
			split = i
		case '/':
			// if we see a path separator, we know that the previously found
			// colon is not the image:tag separator, since a tag cannot have a path separator
			split = -1
		}
	}

	if split == -1 || split == len(fullImg)-1 {
		return fullImg, ""
	}

	return fullImg[:split], fullImg[split+1:]
}
