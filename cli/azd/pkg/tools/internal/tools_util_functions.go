package internal

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"

	"github.com/azure/azure-dev/cli/azd/pkg/executil"
	"github.com/blang/semver/v4"
)

// toolInPath checks to see if a program can be found on the PATH, as exec.LookPath
// does, but returns "(false, nil)" in the case where os.LookPath would return
// exec.ErrNotFound.
func ToolInPath(name string) (bool, error) {
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

func ExecuteCommand(ctx context.Context, cmd string, args ...string) (string, error) {
	runResult, err := executil.RunWithResult(ctx, executil.RunArgs{
		Cmd:  cmd,
		Args: args,
	})
	return runResult.Stdout, err
}

func ExtractSemver(cliOutput string) (semver.Version, error) {
	ver := regexp.MustCompile(`\d+\.\d+\.\d+`).FindString(cliOutput)
	semver, err := semver.Parse(ver)
	if err != nil {
		return semver, err
	}
	return semver, nil
}
