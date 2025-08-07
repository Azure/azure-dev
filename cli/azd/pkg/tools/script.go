// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"io"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
)

// ExecOptions provide configuration for how scripts are executed
type ExecOptions struct {
	Interactive *bool
	StdOut      io.Writer
	UserPwsh    string
	// If true, azd won't try to use alternative shell to execute the command. For example, pwsh won't be tried with
	// powershell (pwsh5) in Windows.
	StrictShell bool
}

// Utility to easily execute a bash script across platforms
type Script interface {
	Execute(ctx context.Context, scriptPath string, options ExecOptions) (exec.RunResult, error)
}
