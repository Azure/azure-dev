// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/blang/semver/v4"
)

type ExternalTool interface {
	CheckInstalled(ctx context.Context) (bool, error)
	InstallUrl() string
	Name() string
}

// toolInPath checks to see if a program can be found on the PATH, as exec.LookPath
// does, but returns "(false, nil)" in the case where os.LookPath would return
// exec.ErrNotFound.
func toolInPath(name string) (bool, error) {
	_, err := exec.LookPath(name)

	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, exec.ErrNotFound):
		return false, nil
	default:
		return false, fmt.Errorf("failed searching for `%s` on PATH: %w", name, err)
	}
}

type ErrSemver struct {
	ToolName    string
	ToolRequire ToolMetaData
}

type ToolMetaData struct {
	MinimumVersion semver.Version
	UpdateCommand  string
}

func (err *ErrSemver) Error() string {
	return fmt.Sprintf("need at least version %s or later of %s installed. %s %s version",
		err.ToolRequire.MinimumVersion.String(), err.ToolName, err.ToolRequire.UpdateCommand, err.ToolName)
}

func versionToSemver(CLIOutput []byte) (semver.Version, error) {
	ver := regexp.MustCompile(`\d+\.\d+\.\d+`).FindString(string(CLIOutput))

	//skip leading zeros
	versionSplit := strings.Split(ver, ".")
	fmt.Println(versionSplit) //DEL
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
