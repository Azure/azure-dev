// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// CaptureOutput
// ---------------------------------------------------------------------------

func TestCaptureOutput_BasicStdout(t *testing.T) {
	stdout, stderr, err := CaptureOutput(func() {
		fmt.Fprint(os.Stdout, "hello stdout")
	})
	require.NoError(t, err)
	require.Equal(t, "hello stdout", stdout)
	require.Empty(t, stderr)
}

func TestCaptureOutput_BasicStderr(t *testing.T) {
	stdout, stderr, err := CaptureOutput(func() {
		fmt.Fprint(os.Stderr, "hello stderr")
	})
	require.NoError(t, err)
	require.Empty(t, stdout)
	require.Equal(t, "hello stderr", stderr)
}

func TestCaptureOutput_BothStreams(t *testing.T) {
	stdout, stderr, err := CaptureOutput(func() {
		fmt.Fprint(os.Stdout, "out")
		fmt.Fprint(os.Stderr, "err")
	})
	require.NoError(t, err)
	require.Equal(t, "out", stdout)
	require.Equal(t, "err", stderr)
}

func TestCaptureOutput_EmptyFunction(t *testing.T) {
	stdout, stderr, err := CaptureOutput(func() {
		// intentionally empty
	})
	require.NoError(t, err)
	require.Empty(t, stdout)
	require.Empty(t, stderr)
}

func TestCaptureOutput_MultilineOutput(t *testing.T) {
	stdout, stderr, err := CaptureOutput(func() {
		fmt.Fprintln(os.Stdout, "line 1")
		fmt.Fprintln(os.Stdout, "line 2")
		fmt.Fprintln(os.Stderr, "error 1")
	})
	require.NoError(t, err)
	require.Equal(t, "line 1\nline 2\n", stdout)
	require.Equal(t, "error 1\n", stderr)
}

func TestCaptureOutput_RestoresOriginalStreams(t *testing.T) {
	origStdout := os.Stdout
	origStderr := os.Stderr

	_, _, err := CaptureOutput(func() {
		fmt.Fprint(os.Stdout, "captured")
	})
	require.NoError(t, err)

	// Verify original streams are restored.
	require.Equal(t, origStdout, os.Stdout)
	require.Equal(t, origStderr, os.Stderr)
}

func TestCaptureOutput_PanicRestoresStreams(t *testing.T) {
	origStdout := os.Stdout
	origStderr := os.Stderr

	require.Panics(t, func() {
		CaptureOutput(func() { //nolint:errcheck // panic prevents return
			fmt.Fprint(os.Stdout, "before panic")
			panic("test panic")
		})
	})

	// Verify original streams are restored even after panic.
	require.Equal(t, origStdout, os.Stdout)
	require.Equal(t, origStderr, os.Stderr)
}
