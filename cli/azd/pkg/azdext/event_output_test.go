// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEventOutput_DefaultsToStdout(t *testing.T) {
	//nolint:forbidigo // Verifies the documented stdout fallback.
	require.Same(t, os.Stdout, EventOutput(t.Context()))
}

func TestEventOutputWriter_ForwardsOutput(t *testing.T) {
	var stdout bytes.Buffer
	var progress []string
	writer := &eventOutputWriter{
		writer: &stdout,
		progress: func(message string) {
			progress = append(progress, message)
		},
	}

	_, err := writer.Write([]byte("warning\n"))
	require.NoError(t, err)
	require.Equal(t, "warning\n", stdout.String())
	require.Equal(t, []string{"warning\n"}, progress)
}

func TestEventOutputWriter_IsSafeWithoutProgress(t *testing.T) {
	var stdout bytes.Buffer
	writer := &eventOutputWriter{writer: &stdout}

	_, err := writer.Write([]byte("status\n"))
	require.NoError(t, err)
	require.Equal(t, "status\n", stdout.String())
}
