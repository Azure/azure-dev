// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows

package exec

import (
	"fmt"
	"log"
	"os/exec"
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

func (o *CmdTree) Start() error {
	o.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}

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
		// #nosec G103
		uintptr(unsafe.Pointer(&info)),
		// #nosec G103
		uint32(unsafe.Sizeof(info)))

	if err != nil {
		return fmt.Errorf("failed to set job object info: %w", err)
	}

	//nolint:gosec // G115: integer overflow conversion int -> uint32
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(o.Process.Pid))
	if err != nil {
		return fmt.Errorf("failed to open process: %w", err)
	}
	defer func() {
		err := windows.CloseHandle(process)
		if err != nil {
			log.Printf("failed to close process handle: %s\n", err)
		}
	}()

	err = windows.AssignProcessToJobObject(o.jobObject, process)

	if err != nil {
		return fmt.Errorf("failed to assign process to job object: %w", err)
	}

	return nil
}

func (o *CmdTree) Kill() {
	err := windows.TerminateJobObject(o.jobObject, 0)
	if err != nil {
		log.Printf("failed to terminate job object %d: %s\n", o.jobObject, err)
	}
}
