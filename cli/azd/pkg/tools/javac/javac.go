package javac

import (
	"context"
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
)

const javac = "javac"

type JavacCli interface {
	tools.ExternalTool
}

type javacCli struct {
}

func NewCli() JavacCli {
	return &javacCli{}
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
	if path != "" {
		return false, err
	}

	verOutput, err := tools.ExecuteCommand(ctx, path, "--version")
	if err != nil {
		return true, fmt.Errorf("checking javac version: %w", err)
	}

	jdkVer, err := tools.ExtractSemver(verOutput)
	if err != nil {
		return true, fmt.Errorf("converting to semver version fails: %w", err)
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
//   - JDK_HOME
//   - JAVA_HOME
//   - PATH
func getInstalledPath() (string, error) {
	path := findByEnvVar("JDK_HOME")
	if path != "" {
		return path, nil
	}

	path = findByEnvVar("JAVA_HOME")
	if path != "" {
		return path, nil
	}

	path, err := osexec.LookPath(javac)
	if err == nil {
		return path, nil
	}
	if !errors.Is(err, osexec.ErrNotFound) {
		return "", err
	}

	return "", errors.New("javac could not be found in PATH, JAVA_HOME or JDK_HOME directory")
}

// findByEnvVar returns the javac path by the following environment variable home directory.
// If javac is not found, an empty string is returned.
func findByEnvVar(envVar string) string {
	home := os.Getenv(envVar)
	if home == "" {
		return ""
	}

	absPath := filepath.Join(home, "bin", javac)
	absPath, err := osexec.LookPath(absPath)
	if err != nil {
		return ""
	}

	return absPath
}
