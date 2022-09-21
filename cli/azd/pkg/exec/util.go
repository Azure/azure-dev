// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exec

import (
	"bytes"
	"context"
	"fmt"
)

// Settings to modify the way CmdTree is executed
type CmdTreeOptions struct {
	Interactive bool
}

// RunCommandList runs a list of commands in shell.
// The command list is constructed using '&&' operator, so the first failing command causes the whole list run to fail.
func RunCommandList(ctx context.Context, commands []string, env []string, cwd string) (RunResult, error) {
	process, err := newCmdTree(ctx, "", commands, true, false)
	if err != nil {
		return NewRunResult(-1, "", ""), err
	}

	process.Cmd.Dir = cwd
	process.Env = appendEnv(env)

	return execCmdTree(process)
}

func execCmdTree(process CmdTree) (RunResult, error) {
	var stdOutBuf bytes.Buffer
	var stdErrBuf bytes.Buffer

	if process.Stdout == nil {
		process.Stdout = &stdOutBuf
	}

	if process.Stderr == nil {
		process.Stderr = &stdErrBuf
	}

	if err := process.Start(); err != nil {
		return NewRunResult(-1, "", ""), fmt.Errorf("error starting process: %w", err)
	}
	defer process.Kill()

	err := process.Wait()

	return NewRunResult(
		process.ProcessState.ExitCode(),
		stdOutBuf.String(),
		stdErrBuf.String(),
	), err
}
