// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows

// cspell:ignore SNAPTHREAD

package process

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	cancellationExitCode    = 1
	windowsControlCExitCode = 0xC000013A
)

type platformState struct {
	ready     chan struct{}
	readyOnce sync.Once

	mu                       sync.Mutex
	jobObject                windows.Handle
	setupErr                 error
	resumeAfterJobAssignment bool
}

func configure(t *Tree) {
	t.platform.ready = make(chan struct{})

	if t.Cmd.SysProcAttr == nil {
		t.Cmd.SysProcAttr = &syscall.SysProcAttr{}
	} else {
		attributes := *t.Cmd.SysProcAttr
		t.Cmd.SysProcAttr = &attributes
	}

	if !t.options.ForwardInterrupts {
		t.Cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
	}
	// Keep the initial thread from running until the process is assigned to the
	// Job Object. Go 1.26 does not expose atomic job-list process creation.
	if t.Cmd.SysProcAttr.CreationFlags&windows.CREATE_SUSPENDED == 0 {
		t.Cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
		t.platform.resumeAfterJobAssignment = true
	}
}

func finishStart(t *Tree) (err error) {
	var jobObject windows.Handle

	defer func() {
		if err != nil && jobObject != 0 {
			if closeErr := windows.CloseHandle(jobObject); closeErr != nil {
				err = errors.Join(
					err,
					fmt.Errorf("closing job object after setup failure: %w", closeErr),
				)
			}
		}

		t.platform.mu.Lock()
		t.platform.setupErr = err
		if err == nil {
			t.platform.jobObject = jobObject
		}
		t.platform.mu.Unlock()

		t.platform.readyOnce.Do(func() {
			close(t.platform.ready)
		})
	}()

	jobObject, err = windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("creating job object: %w", err)
	}
	if err = setKillOnJobClose(jobObject, true); err != nil {
		return fmt.Errorf("configuring job object: %w", err)
	}

	var assignErr error
	if err = t.Cmd.Process.WithHandle(func(handle uintptr) {
		assignErr = windows.AssignProcessToJobObject(jobObject, windows.Handle(handle))
	}); err != nil {
		return fmt.Errorf("accessing process handle for job assignment: %w", err)
	}
	if assignErr != nil {
		return fmt.Errorf("assigning process to job object: %w", assignErr)
	}

	if t.platform.resumeAfterJobAssignment {
		//nolint:gosec // G115: Windows process IDs are uint32 values
		if err = resumeInitialThread(uint32(t.Cmd.Process.Pid)); err != nil {
			return fmt.Errorf("resuming process after job assignment: %w", err)
		}
	}

	return nil
}

func resumeInitialThread(processID uint32) (err error) {
	threadID, err := initialThreadID(processID)
	if err != nil {
		return err
	}

	thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, threadID)
	if err != nil {
		return fmt.Errorf("opening initial thread: %w", err)
	}
	defer func() {
		if closeErr := windows.CloseHandle(thread); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("closing initial thread handle: %w", closeErr))
		}
	}()

	if _, err = windows.ResumeThread(thread); err != nil {
		return fmt.Errorf("resuming initial thread: %w", err)
	}

	return nil
}

func initialThreadID(processID uint32) (threadID uint32, err error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return 0, fmt.Errorf("taking thread snapshot: %w", err)
	}
	defer func() {
		if closeErr := windows.CloseHandle(snapshot); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("closing thread snapshot: %w", closeErr))
		}
	}()

	entry := windows.ThreadEntry32{
		Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{})),
	}
	if err = windows.Thread32First(snapshot, &entry); err != nil {
		return 0, fmt.Errorf("reading first thread snapshot entry: %w", err)
	}

	for {
		if entry.OwnerProcessID == processID {
			// A CREATE_SUSPENDED process cannot create additional threads itself.
			// Refuse ambiguity rather than resuming an externally created thread.
			if threadID != 0 {
				return 0, fmt.Errorf("found multiple initial thread candidates for process %d", processID)
			}
			threadID = entry.ThreadID
		}

		err = windows.Thread32Next(snapshot, &entry)
		if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("reading next thread snapshot entry: %w", err)
		}
	}

	if threadID == 0 {
		return 0, fmt.Errorf("initial thread for process %d was not found", processID)
	}

	return threadID, nil
}

func startFailed(t *Tree, err error) {
	t.platform.mu.Lock()
	t.platform.setupErr = err
	t.platform.mu.Unlock()
	t.platform.readyOnce.Do(func() {
		close(t.platform.ready)
	})
}

// IsInterruptExit reports whether an exit status represents a Ctrl+C interrupt.
func IsInterruptExit(_ *exec.ExitError, exitCode int) bool {
	return IsInterruptExitCode(exitCode)
}

// IsInterruptExitCode reports whether exitCode represents a Windows Ctrl+C interrupt.
func IsInterruptExitCode(exitCode int) bool {
	return exitCode == 130 ||
		uint32(exitCode) == windowsControlCExitCode //nolint:gosec // preserve the Windows exit code bit pattern
}

func terminate(t *Tree) error {
	return terminateTree(t, false)
}

func terminateRemaining(t *Tree) error {
	return terminateTree(t, true)
}

func terminateOnWait(t *Tree) error {
	return terminateTree(t, true)
}

func terminateTree(t *Tree, allowFinished bool) error {
	<-t.platform.ready

	t.platform.mu.Lock()
	defer t.platform.mu.Unlock()

	if !allowFinished && t.finished.Load() {
		return os.ErrProcessDone
	}
	if t.platform.setupErr != nil ||
		t.platform.jobObject == 0 {
		if t.Cmd.Process == nil {
			return os.ErrProcessDone
		}
		return t.Cmd.Process.Kill()
	}

	activeProcesses, err := activeProcessCount(t.platform.jobObject)
	if err != nil {
		return fmt.Errorf("checking job state: %w", err)
	}
	if activeProcesses == 0 {
		return os.ErrProcessDone
	}

	if err := windows.TerminateJobObject(t.platform.jobObject, cancellationExitCode); err != nil {
		return fmt.Errorf("terminating job object: %w", err)
	}

	return nil
}

func release(t *Tree, killRemaining bool) error {
	<-t.platform.ready

	t.platform.mu.Lock()
	defer t.platform.mu.Unlock()

	var errs []error
	if t.platform.jobObject != 0 && !killRemaining {
		if err := setKillOnJobClose(t.platform.jobObject, false); err != nil {
			errs = append(errs, fmt.Errorf("disabling job close termination: %w", err))
		}
	}
	if t.platform.jobObject != 0 {
		if err := windows.CloseHandle(t.platform.jobObject); err != nil {
			errs = append(errs, fmt.Errorf("closing job object: %w", err))
		}
		t.platform.jobObject = 0
	}

	return errors.Join(errs...)
}

type jobObjectBasicAccountingInformation struct {
	totalUserTime             int64
	totalKernelTime           int64
	thisPeriodTotalUserTime   int64
	thisPeriodTotalKernelTime int64
	totalPageFaultCount       uint32
	totalProcesses            uint32
	activeProcesses           uint32
	totalTerminatedProcesses  uint32
}

func activeProcessCount(handle windows.Handle) (uint32, error) {
	var info jobObjectBasicAccountingInformation
	err := windows.QueryInformationJobObject(
		handle,
		windows.JobObjectBasicAccountingInformation,
		// #nosec G103
		uintptr(unsafe.Pointer(&info)),
		// #nosec G103
		uint32(unsafe.Sizeof(info)),
		nil,
	)
	return info.activeProcesses, err
}

func setKillOnJobClose(handle windows.Handle, enabled bool) error {
	var limitFlags uint32
	if enabled {
		limitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	}

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: limitFlags,
		},
	}
	_, err := windows.SetInformationJobObject(
		handle,
		windows.JobObjectExtendedLimitInformation,
		// #nosec G103
		uintptr(unsafe.Pointer(&info)),
		// #nosec G103
		uint32(unsafe.Sizeof(info)),
	)
	return err
}
