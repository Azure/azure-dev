// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bash

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

// NewExecutor creates a bash HookExecutor. Takes only IoC-injectable deps.
func NewExecutor(commandRunner exec.CommandRunner) tools.HookExecutor {
	return &bashExecutor{commandRunner: commandRunner}
}

type bashExecutor struct {
	commandRunner exec.CommandRunner
	tempFile      string // temp script created from inline content
}

// Prepare creates a temp script file when the execution context
// carries inline script content. For file-based hooks this is a no-op.
func (b *bashExecutor) Prepare(
	_ context.Context, _ string, execCtx tools.ExecutionContext,
) error {
	if execCtx.InlineScript == "" {
		return nil
	}

	tmpFile, err := os.CreateTemp(
		"", fmt.Sprintf("azd-%s-*.sh", execCtx.HookName),
	)
	if err != nil {
		return fmt.Errorf("creating temp script: %w", err)
	}

	content := "#!/bin/sh\nset -e\n\n" +
		"# Auto generated file from Azure Developer CLI\n" +
		execCtx.InlineScript + "\n"

	if err := os.WriteFile(
		tmpFile.Name(), []byte(content), osutil.PermissionExecutableFile,
	); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return fmt.Errorf("writing temp script: %w", err)
	}

	// os.WriteFile only applies perm when *creating* a new file.
	// The file already exists from CreateTemp (mode 0600), so we
	// must explicitly chmod to add execute permission for Unix.
	if err := os.Chmod(
		tmpFile.Name(), osutil.PermissionExecutableFile,
	); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return fmt.Errorf("setting temp script permissions: %w", err)
	}

	tmpFile.Close()
	b.tempFile = tmpFile.Name()

	return nil
}

// Execute runs the specified bash script. When a temp file was
// created during Prepare it is used instead of the provided path.
// When interactive is true will attach to stdin, stdout & stderr.
func (b *bashExecutor) Execute(
	ctx context.Context, path string, execCtx tools.ExecutionContext,
) (exec.RunResult, error) {
	if b.tempFile != "" {
		path = b.tempFile
	}

	var runArgs exec.RunArgs
	// Bash likes all path separators in POSIX format
	path = strings.ReplaceAll(path, "\\", "/")

	if runtime.GOOS == "windows" {
		runArgs = exec.NewRunArgs("bash", path)
	} else {
		runArgs = exec.NewRunArgs("", path)
	}

	runArgs = runArgs.
		WithCwd(execCtx.Cwd).
		WithEnv(execCtx.EnvVars).
		WithShell(true)

	if execCtx.Interactive != nil {
		runArgs = runArgs.WithInteractive(*execCtx.Interactive)
	}

	if execCtx.StdOut != nil {
		runArgs = runArgs.WithStdOut(execCtx.StdOut)
	}

	return b.commandRunner.Run(ctx, runArgs)
}

// Cleanup removes any temporary script file created during Prepare.
func (b *bashExecutor) Cleanup(_ context.Context) error {
	if b.tempFile != "" {
		err := os.Remove(b.tempFile)
		b.tempFile = ""
		return err
	}
	return nil
}
