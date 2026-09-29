// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

// The notice naming the log file is terminal output, so it belongs on the
// writer the caller injected. On process stderr an embedder cannot capture it
// and two commands in one process interleave on it.
func TestTheDebugNoticeGoesToTheInjectedWriter(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Bool("debug", true, "")

	var errOut bytes.Buffer
	restore := setupDebugLogging(flags, &errOut)
	t.Cleanup(restore)

	require.Contains(t, errOut.String(), "Debug log:",
		"the caller's writer is where the notice belongs")
}

// Nothing is written when debug was not asked for, so an ordinary command's
// stderr stays clean.
func TestNoDebugNoticeWithoutDebug(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Bool("debug", false, "")

	var errOut bytes.Buffer
	restore := setupDebugLogging(flags, &errOut)
	t.Cleanup(restore)

	require.Empty(t, strings.TrimSpace(errOut.String()))
}

// The root hook has to hand the command's writer down, or the plumbing above is
// wired to nothing. The bug is a wrong argument, so the call site is what is
// pinned -- passing a buffer here would prove only that the parameter works.
func TestRootPassesItsErrorWriterToDebugSetup(t *testing.T) {
	require.True(t, callPassesCommandErrWriter(t, "root.go", "setupDebugLogging"),
		"setupDebugLogging must be called with cmd.ErrOrStderr()")
}
