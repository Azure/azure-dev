// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows
// +build windows

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

type winProcess struct {
	_    uint32 //pid
	hndl windows.Handle
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
