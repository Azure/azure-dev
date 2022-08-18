// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"fmt"

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
