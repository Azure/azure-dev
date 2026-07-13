// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"io"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

func Test_NewRestoreAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &restoreFlags{}
	console := mockinput.NewMockConsole()
	formatter := &output.JsonFormatter{}
	a := newRestoreAction(
		flags, nil, console, formatter, io.Discard,
		nil, nil, nil, nil, nil, nil, nil,
	)
	ra := a.(*restoreAction)
	require.Same(t, flags, ra.flags)
}

func Test_NewRestoreCmd(t *testing.T) {
	t.Parallel()
	cmd := newRestoreCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "restore")
}

func Test_NewRestoreFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newRestoreFlags(cmd, global)
	require.NotNil(t, flags)
}
