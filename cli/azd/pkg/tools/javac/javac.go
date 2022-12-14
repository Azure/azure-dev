package javac

import (
	"context"
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
)

const javac = "javac"

type JavacCli interface {
	tools.ExternalTool
}

type javacCli struct {
	cmdRun exec.CommandRunner
}

func NewCli(cmdRun exec.CommandRunner) JavacCli {
	return &javacCli{
		cmdRun: cmdRun,
	}
}

func (j *javacCli) VersionInfo() tools.VersionInfo {
	return tools.VersionInfo{
		MinimumVersion: semver.Version{
			Major: 17,
			Minor: 0,
			Patch: 0},
		UpdateCommand: "Visit the website for your installed JDK to upgrade",
	}
}

func (j *javacCli) CheckInstalled(ctx context.Context) (bool, error) {
	path, err := getInstalledPath()
	if err != nil {
		return false, err
	}

	runResult, err := j.cmdRun.Run(ctx, exec.RunArgs{
		Cmd:  path,
		Args: []string{"--version"},
	})
	if err != nil {
		// On older versions of javac (8 and below), `javac -version` is supported instead.
		// If this returns successfully, we know that it's an older version
		// and can safely recommend an upgrade.
		_, err := j.cmdRun.Run(ctx, exec.RunArgs{
			Cmd:  path,
			Args: []string{"-version"},
		})

		if err == nil {
			return false, &tools.ErrSemver{ToolName: j.Name(), VersionInfo: j.VersionInfo()}
		}

		return false, fmt.Errorf("checking javac version: %w", err)
	}

	jdkVer, err := tools.ExtractVersion(runResult.Stdout)
	if err != nil {
		return false, fmt.Errorf("converting to semver version fails: %w", err)
	}

	requiredVersion := j.VersionInfo()
	if jdkVer.LT(requiredVersion.MinimumVersion) {
		return false, &tools.ErrSemver{ToolName: j.Name(), VersionInfo: requiredVersion}
	}

	return true, nil
}

func (j *javacCli) InstallUrl() string {
	return "https://www.microsoft.com/openjdk"
}

func (j *javacCli) Name() string {
	return "Java JDK"
}

// getInstalledPath returns the installed javac path.
//
// javac is located by consulting, in search order:
//   - JAVA_HOME (if set)
//   - PATH
//
// An error is returned if javac could not be found, or if invalid locations are provided.
func getInstalledPath() (string, error) {
	path, err := findByEnvVar("JAVA_HOME")
	if path != "" {
		return path, nil
	}
	if err != nil {
		return "", fmt.Errorf("JAVA_HOME is set to an invalid directory: %w", err)
	}

	path, err = osexec.LookPath(javac)
	if err == nil {
		return path, nil
	}

	if !errors.Is(err, osexec.ErrNotFound) {
		return "", fmt.Errorf("failed looking up javac in PATH: %w", err)
	}

	return "", errors.New(
		"javac could not be found. Set JAVA_HOME environment variable to point to your Java JDK installation, " +
			"or include javac in your PATH environment variable")
}

// findByEnvVar returns the javac path by the following environment variable home directory.
//
// An error is returned if an error occurred while finding.
// If the environment variable home directory is unset, an empty string is returned with no error.
func findByEnvVar(envVar string) (string, error) {
	home := os.Getenv(envVar)
	if home == "" {
		return "", nil
	}

	absPath := filepath.Join(home, "bin", javac)
	absPath, err := osexec.LookPath(absPath)
	if err != nil {
		return "", err
	}

	return absPath, nil
}
