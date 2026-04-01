// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sync"
)

// CaptureOutput captures stdout and stderr output from a function.
// Useful for testing CLI output.
//
// The function fn is called synchronously. Any writes to os.Stdout and
// os.Stderr during fn's execution are captured and returned as strings.
// The original file descriptors are restored after fn returns, even if
// fn panics.
func CaptureOutput(fn func()) (string, string, error) {
	origStdout := os.Stdout
	origStderr := os.Stderr

	outR, outW, err := os.Pipe()
	if err != nil {
		return "", "", fmt.Errorf("azdext.CaptureOutput: failed to create stdout pipe: %w", err)
	}

	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()

		return "", "", fmt.Errorf("azdext.CaptureOutput: failed to create stderr pipe: %w", err)
	}

	os.Stdout = outW
	os.Stderr = errW

	var outBuf, errBuf bytes.Buffer
	var wg sync.WaitGroup

	wg.Add(2) //nolint:mnd // reading from exactly 2 pipes

	go func() {
		defer wg.Done()
		io.Copy(&outBuf, outR) //nolint:errcheck // best-effort capture
	}()

	go func() {
		defer wg.Done()
		io.Copy(&errBuf, errR) //nolint:errcheck // best-effort capture
	}()

	// Deferred cleanup ensures pipes are closed and goroutines drained
	// even if fn panics. On the normal path the explicit Close calls
	// below run first; duplicate Close on an *os.File is harmless.
	defer func() {
		os.Stdout = origStdout
		os.Stderr = origStderr
		outW.Close()
		errW.Close()
		wg.Wait()
		outR.Close()
		errR.Close()
	}()

	fn()

	// Close writers so readers reach EOF.
	outW.Close()
	errW.Close()

	// Wait for readers to finish draining.
	wg.Wait()

	outR.Close()
	errR.Close()

	return outBuf.String(), errBuf.String(), nil
}
