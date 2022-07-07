// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
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
