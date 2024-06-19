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

type Docker interface {
	tools.ExternalTool
	Login(ctx context.Context, loginServer string, username string, password string) error
	Build(
		ctx context.Context,
		cwd string,
		dockerFilePath string,
		platform string,
		target string,
		buildContext string,
		name string,
		buildArgs []string,
		buildSecrets []string,
		buildEnv []string,
		buildProgress io.Writer,
	) (string, error)
	Tag(ctx context.Context, cwd string, imageName string, tag string) error
	Push(ctx context.Context, cwd string, tag string) error
	Pull(ctx context.Context, imageName string) error
	Inspect(ctx context.Context, imageName string, format string) (string, error)
}

func NewDocker(commandRunner exec.CommandRunner) Docker {
	return &docker{
		commandRunner: commandRunner,
	}
}

type docker struct {
	commandRunner exec.CommandRunner
}

func (d *docker) Login(ctx context.Context, loginServer string, username string, password string) error {
	runArgs := exec.NewRunArgs(
		"docker", "login",
		"--username", username,
		"--password-stdin",
		loginServer,
	).WithStdIn(strings.NewReader(password))

	_, err := d.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("failed logging into docker: %w", err)
	}

	return nil
}

// Runs a Docker build for a given Dockerfile, writing the output of docker build to [stdOut] when it is
// not nil. If the platform is not specified (empty) it defaults to amd64. If the build is successful,
// the function returns the image id of the built image.
func (d *docker) Build(
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
	runArgs := exec.NewRunArgs("docker", args...).WithCwd(cwd).WithEnv(buildEnv)

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

func (d *docker) Tag(ctx context.Context, cwd string, imageName string, tag string) error {
	_, err := d.executeCommand(ctx, cwd, "tag", imageName, tag)
	if err != nil {
		return fmt.Errorf("tagging image: %w", err)
	}

	return nil
}

func (d *docker) Push(ctx context.Context, cwd string, tag string) error {
	_, err := d.executeCommand(ctx, cwd, "push", tag)
	if err != nil {
		return fmt.Errorf("pushing image: %w", err)
	}

	return nil
}

func (d *docker) Pull(ctx context.Context, imageName string) error {
	_, err := d.executeCommand(ctx, "", "pull", imageName)
	if err != nil {
		return fmt.Errorf("pulling image: %w", err)
	}

	return nil
}

func (d *docker) Inspect(ctx context.Context, imageName string, format string) (string, error) {
	out, err := d.executeCommand(ctx, "", "image", "inspect", "--format", format, imageName)
	if err != nil {
		return "", fmt.Errorf("inspecting image: %w", err)
	}

	return out.Stdout, nil
}

func (d *docker) versionInfo() tools.VersionInfo {
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
func (d *docker) CheckInstalled(ctx context.Context) error {
	err := tools.ToolInPath("docker")
	if err != nil {
		return err
	}
	dockerRes, err := tools.ExecuteCommand(ctx, d.commandRunner, "docker", "--version")
	if err != nil {
		return fmt.Errorf("checking %s version: %w", d.Name(), err)
	}
	log.Printf("docker version: %s", dockerRes)
	supported, err := isSupportedDockerVersion(dockerRes)
	if err != nil {
		return err
	}
	if !supported {
		return &tools.ErrSemver{ToolName: d.Name(), VersionInfo: d.versionInfo()}
	}
	return nil
}

func (d *docker) InstallUrl() string {
	return "https://aka.ms/azure-dev/docker-install"
}

func (d *docker) Name() string {
	return "Docker"
}

func (d *docker) executeCommand(ctx context.Context, cwd string, args ...string) (exec.RunResult, error) {
	runArgs := exec.NewRunArgs("docker", args...).
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
