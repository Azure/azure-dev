// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows

package osutil

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

const (
	readFileRetryBudget = 250 * time.Millisecond
	readFileMaxDelay    = 16 * time.Millisecond
)

// ReadFile reads the named file, retrying transient Windows sharing contention.
func ReadFile(path string) ([]byte, error) {
	deadline := time.Now().Add(readFileRetryBudget)
	delay := time.Millisecond

	for {
		data, err := os.ReadFile(path)
		if err == nil || !isReadFileContention(err) || time.Now().After(deadline) {
			return data, err
		}

		time.Sleep(delay)
		delay = min(delay*2, readFileMaxDelay)
	}
}

func isReadFileContention(err error) bool {
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return false
	}

	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
