// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/ux/internal"
	"github.com/stretchr/testify/require"
)

func TestMultiSelect_InputUsesConfiguredWriter(t *testing.T) {
	// A pipe makes terminal setup fail deterministically without reading the user's terminal.
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	stdin := os.Stdin
	os.Stdin = reader
	t.Cleanup(func() {
		os.Stdin = stdin
		require.NoError(t, reader.Close())
		require.NoError(t, writer.Close())
	})

	var output bytes.Buffer
	prompt := NewMultiSelect(&MultiSelectOptions{Writer: &output})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	err = prompt.input.ReadInput(ctx, nil, func(*internal.KeyPressEventArgs) (bool, error) {
		return false, nil
	})
	require.Error(t, err)
	require.NoError(t, ctx.Err(), "terminal setup must fail without waiting for the timeout")
	require.Equal(t, "\x1b[?25h", output.String(), "input cursor output must use the prompt writer")
}
