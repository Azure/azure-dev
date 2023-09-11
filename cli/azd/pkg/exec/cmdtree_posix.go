// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows
// +build !windows

package exec

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// newCmdTree creates a `CmdTree`, optionally using a shell appropriate for windows
// or POSIX environments.
// An empty cmd parameter indicates "command list mode", which means that args are combined into a single command list,
// joined with && operator.
func newCmdTree(ctx context.Context, cmd string, args []string, useShell bool, interactive bool) (CmdTree, error) {
	options := CmdTreeOptions{Interactive: interactive}

	if !useShell {
		if cmd == "" {
			return CmdTree{}, errors.New("command must be provided if shell is not used")
		} else {
			return CmdTree{
				CmdTreeOptions: options,
				Cmd:            exec.CommandContext(ctx, cmd, args...),
			}, nil
		}
	}

	var shellName string
	var shellCommandPrefix []string

	shellName = filepath.Join("/", "bin", "sh")
	shellCommandPrefix = []string{"-c"}

	if cmd == "" {
		args = []string{strings.Join(args, " && ")}
	} else {
		var cmdBuilder strings.Builder
		cmdBuilder.WriteString(cmd)

		for i := range args {
			cmdBuilder.WriteString(" \"$")
			fmt.Fprintf(&cmdBuilder, "%d", i)
			cmdBuilder.WriteString("\"")
		}

		args = append([]string{cmdBuilder.String()}, args...)
	}

	var allArgs []string
	allArgs = append(allArgs, shellCommandPrefix...)
	allArgs = append(allArgs, args...)

	return CmdTree{
		CmdTreeOptions: options,
		Cmd:            exec.Command(shellName, allArgs...),
	}, nil
}

// CmdTree represents an `exec.Cmd` run inside a process group. When
// `Kill` is called, SIGKILL is sent to the process group, which will
// kill any lingering child processes launched by the root process.
type CmdTree struct {
	CmdTreeOptions
	*exec.Cmd
}

func (o *CmdTree) Start() error {
	// Interactive commands like `gh auth login` should be created as
	// a fork child process in posix OS so it can use the same stdin
	// Non interactive commands like `gh auth status` can be spawn
	// wit a new process group (not as a child process) as it won't
	// require stdin to interact with the user
	if !o.Interactive {
		if o.Cmd.SysProcAttr == nil {
			o.Cmd.SysProcAttr = &syscall.SysProcAttr{}
		}

		o.Cmd.SysProcAttr.Setpgid = true
	}

	return o.Cmd.Start()
}

func (o *CmdTree) Kill() {
	_ = syscall.Kill(-o.Cmd.Process.Pid, syscall.SIGKILL)
}
