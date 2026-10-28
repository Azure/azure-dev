// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows

// cspell:ignore MOVEFILE

package cmd

import (
	"errors"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

const agentStateReplaceRetryBudget = 500 * time.Millisecond

func replaceAgentStateFile(from, to string) error {
	fromPtr, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	toPtr, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}

	deadline := time.Now().Add(agentStateReplaceRetryBudget)
	delay := time.Millisecond
	for {
		err = windows.MoveFileEx(
			fromPtr,
			toPtr,
			windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH,
		)
		if err == nil || !isAgentStateSharingContention(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(delay)
		if delay < 16*time.Millisecond {
			delay *= 2
		}
	}
}

func isAgentStateSharingContention(err error) bool {
	errno, ok := errors.AsType[syscall.Errno](err)
	return ok && (errno == windows.ERROR_ACCESS_DENIED || errno == windows.ERROR_SHARING_VIOLATION)
}
