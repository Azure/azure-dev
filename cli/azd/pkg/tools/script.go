// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

// ExecutionContext provides the per-invocation execution environment
// for a hook. The HooksRunner constructs this from the validated
// HookConfig, resolving secrets, merging directories, and building
// the environment.
type ExecutionContext struct {
	// Cwd is the working directory for execution.
	Cwd string

	// EnvVars is the merged environment for the process, including
	// resolved secrets and the azd environment.
	EnvVars []string

	// BoundaryDir is the project or service root directory.
	// Executors may walk upward from the script to BoundaryDir
	// to discover dependency files (requirements.txt, package.json).
	BoundaryDir string

	// Interactive controls whether stdin is attached.
	Interactive *bool

	// StdOut overrides the default stdout for the process.
	StdOut io.Writer

	// InlineScript contains the raw script content for inline hooks.
	// When set, the executor creates a temp file in Prepare() with
	// the appropriate extension and content wrapper (e.g., shebang
	// for bash). When empty, scriptPath is used directly as a file
	// path.
	InlineScript string

	// HookName is the descriptive name of the hook (e.g.,
	// "preprovision"). Used by executors for temp file naming to
	// aid debuggability.
	HookName string

	// Config is the executor-specific property bag from HookConfig.
	// Executors can unmarshal this into a typed struct for their
	// configuration needs. May be nil or empty if no config was
	// specified. Executors must not mutate this map — it is shared
	// with the underlying HookConfig.
	Config map[string]any
}

// HookExecutor is the unified interface for all hook execution.
// Every executor follows a three-phase lifecycle:
//  1. Prepare — validate prerequisites, resolve tools, create temp files
//  2. Execute — run the hook
//  3. Cleanup — remove temporary resources created during Prepare
type HookExecutor interface {
	// Prepare performs pre-execution setup such as runtime validation,
	// virtual environment creation, dependency installation, or temp
	// file creation for inline scripts.
	Prepare(ctx context.Context, scriptPath string, execCtx ExecutionContext) error

	// Execute runs the hook at the given path.
	Execute(ctx context.Context, scriptPath string, execCtx ExecutionContext) (exec.RunResult, error)

	// Cleanup removes any temporary resources created during Prepare.
	// Called by the hooks runner after Execute completes, regardless
	// of success or failure. Implementations must be safe to call
	// even when Prepare was not called or created no resources.
	Cleanup(ctx context.Context) error
}

// CreateInlineTempScript creates a temp file for an inline hook
// script with executable permissions. The caller is responsible
// for cleaning up the returned file.
func CreateInlineTempScript(
	hookName, ext, content string,
) (string, error) {
	file, err := os.CreateTemp(
		"", fmt.Sprintf("azd-%s-*%s", hookName, ext),
	)
	if err != nil {
		return "", fmt.Errorf(
			"failed creating temp file: %w", err,
		)
	}
	filePath := file.Name()
	file.Close()

	if err := os.WriteFile(
		filePath, []byte(content),
		osutil.PermissionExecutableFile,
	); err != nil {
		os.Remove(filePath)
		return "", fmt.Errorf(
			"failed writing temp script: %w", err,
		)
	}

	if err := os.Chmod(
		filePath, osutil.PermissionExecutableFile,
	); err != nil {
		os.Remove(filePath)
		return "", fmt.Errorf(
			"failed setting executable permission: %w",
			err,
		)
	}

	return filePath, nil
}
