// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"errors"
	"fmt"
	osexec "os/exec"
	"regexp"
	"strconv"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/blang/semver/v4"
)

type ExternalTool interface {
	CheckInstalled(ctx context.Context) (bool, error)
	InstallUrl() string
	Name() string
}

type ErrSemver struct {
	ToolName    string
	VersionInfo VersionInfo
}

type VersionInfo struct {
	MinimumVersion semver.Version
	UpdateCommand  string
}

func (err *ErrSemver) Error() string {
	return fmt.Sprintf("need at least version %s or later of %s installed. %s %s version",
		err.VersionInfo.MinimumVersion.String(), err.ToolName, err.VersionInfo.UpdateCommand, err.ToolName)
}

// toolInPath checks to see if a program can be found on the PATH, as exec.LookPath
// does, but returns "(false, nil)" in the case where os.LookPath would return
// exec.ErrNotFound.
func ToolInPath(name string) (bool, error) {
	_, err := osexec.LookPath(name)

	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, osexec.ErrNotFound):
		return false, nil
	default:
		return false, fmt.Errorf("failed searching for `%s` on PATH: %w", name, err)
	}
}

func ExecuteCommand(ctx context.Context, commandRunner exec.CommandRunner, cmd string, args ...string) (string, error) {
	runResult, err := commandRunner.Run(ctx, exec.RunArgs{
		Cmd:  cmd,
		Args: args,
	})
	return runResult.Stdout, err
}

// ExtractVersion extracts a major.minor.patch version number from a typical CLI version flag output.
//
// minor and patch version numbers are both optional, treated as 0 if not found.
func ExtractVersion(cliOutput string) (semver.Version, error) {
	majorMinorPatch := regexp.MustCompile(`\d+\.\d+\.\d+`).FindString(cliOutput)
	ver, err := semver.Parse(majorMinorPatch)
	if err == nil {
		return ver, nil
	}

	majorMinor := regexp.MustCompile(`(\d+)\.(\d+)`).FindStringSubmatch(cliOutput)
	if len(majorMinor) >= 3 {
		return semver.Version{
			Major: parseUint(majorMinor[1]),
			Minor: parseUint(majorMinor[2]),
		}, nil
	}

	major := regexp.MustCompile(`\d+`).FindString(cliOutput)
	if major != "" {
		return semver.Version{Major: parseUint(major)}, nil
	}

	return semver.Version{}, fmt.Errorf("no valid version number found in %s", cliOutput)
}

func parseUint(s string) uint64 {
	res, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		panic(err)
	}
	return res
}
