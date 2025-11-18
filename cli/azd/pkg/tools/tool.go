// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"fmt"
	"regexp"
	"strconv"

	"github.com/azure/azure-dev/pkg/exec"
	"github.com/blang/semver/v4"
)

type ExternalTool interface {
	CheckInstalled(ctx context.Context) error
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
