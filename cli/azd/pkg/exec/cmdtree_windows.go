// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows
// +build windows

package exec

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// CmdTree represents an `exec.Cmd` run inside a windows Job object. When
// `Kill` is called, the entire job is terminated, which will kill any lingering
// child processes launched by the root process.
type CmdTree struct {
	CmdTreeOptions
	*exec.Cmd
	jobObject windows.Handle
}

type winProcess struct {
	_    uint32 //pid
	hndl windows.Handle
}

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

	dir := os.Getenv("SYSTEMROOT")
	if dir == "" {
		return CmdTree{}, errors.New("environment variable 'SYSTEMROOT' has no value")
	}

	shellName = filepath.Join(dir, "System32", "cmd.exe")

	execCmd := exec.CommandContext(ctx, shellName)

	execCmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: buildCmdCmdLine(cmd, args),
	}

	return CmdTree{
		CmdTreeOptions: options,
		Cmd:            execCmd,
	}, nil
}

func (o *CmdTree) Start() error {
	if o.SysProcAttr == nil {
		o.SysProcAttr = &syscall.SysProcAttr{}
	}

	o.SysProcAttr.CreationFlags = syscall.CREATE_NEW_PROCESS_GROUP

	err := o.Cmd.Start()
	if err != nil {
		return err
	}

	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("failed to create job object: %w", err)
	}

	o.jobObject = handle

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	_, err = windows.SetInformationJobObject(
		handle,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)))

	if err != nil {
		return fmt.Errorf("failed to set job object info: %w", err)
	}

	err = windows.AssignProcessToJobObject(
		o.jobObject,
		(*winProcess)(unsafe.Pointer(o.Process)).hndl)

	if err != nil {
		return fmt.Errorf("failed to assign process to job object: %w", err)
	}

	return nil
}

func (o *CmdTree) Kill() {
	err := windows.TerminateJobObject(windows.Handle(o.jobObject), 0)
	if err != nil {
		log.Printf("failed to terminate job object %d: %s\n", o.jobObject, err)
	}
}

// buildCmdCmdLine builds the command line for`cmd.exe` to have it run command or list of commands. When cmd is non empty
// it is treated as the command to run and args is the array of arguments for cmd. Otherwise, each arg is treated as an
// individual command, and joined with &&.  Each individual command is quoted.
func buildCmdCmdLine(cmd string, args []string) string {
	var cmdLine strings.Builder
	cmdLine.WriteString("/c ")
	cmdLine.WriteString(`"`)
	if cmd == "" {
		for idx, arg := range args {
			if idx != 0 {
				cmdLine.WriteString("  &&  ")
			}
			cmdLine.WriteString(`"`)
			cmdLine.WriteString(arg)
			cmdLine.WriteString(`"`)
		}
	} else {
		cmdLine.WriteString(`"`)
		cmdLine.WriteString(cmd)
		cmdLine.WriteString(`"`)
		for _, arg := range args {
			cmdLine.WriteString(" ")
			cmdLine.WriteString(`"`)
			cmdLine.WriteString(arg)
			cmdLine.WriteString(`"`)
		}
	}
	cmdLine.WriteString(`"`)

	return cmdLine.String()
}
