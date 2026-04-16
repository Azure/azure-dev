// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows

package scripting

import (
	"log"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// setupProcessTree merges CREATE_NEW_PROCESS_GROUP into the command's
// SysProcAttr (preserving CmdLine if already set by setCmdLineOverride).
func setupProcessTree(cmd *exec.Cmd, _ bool) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}

// startProcessTree starts the command, creates a Windows Job Object with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE, and assigns the process to it.
// The returned kill function terminates the entire job (all child processes).
func startProcessTree(cmd *exec.Cmd) (kill func(), _ error) {
	if err := cmd.Start(); err != nil {
		return func() {}, err
	}

	// Best-effort Job Object setup — fall back to direct process kill.
	fallbackKill := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}

	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		log.Printf("scripting: Job Object creation failed, falling back to direct kill: %s", err)
		return fallbackKill, nil
	}

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	//nolint:gosec // G103: unsafe.Pointer needed for Windows API
	_, err = windows.SetInformationJobObject(
		handle,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err != nil {
		log.Printf("scripting: Job Object configuration failed, falling back to direct kill: %s", err)
		_ = windows.CloseHandle(handle)
		return fallbackKill, nil
	}

	//nolint:gosec // G115: int-to-uint32 conversion safe for PIDs
	proc, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		log.Printf("scripting: Job Object process open failed, falling back to direct kill: %s", err)
		_ = windows.CloseHandle(handle)
		return fallbackKill, nil
	}

	if err := windows.AssignProcessToJobObject(handle, proc); err != nil {
		log.Printf("scripting: Job Object process assignment failed, falling back to direct kill: %s", err)
		_ = windows.CloseHandle(proc)
		_ = windows.CloseHandle(handle)
		return fallbackKill, nil
	}
	_ = windows.CloseHandle(proc)

	return func() {
		if err := windows.TerminateJobObject(handle, 1); err != nil {
			log.Printf("scripting: failed to terminate job object: %s\n", err)
		}
	}, nil
}
