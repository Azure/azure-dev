// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"io"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

func Test_NewAuthStatusAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &authStatusFlags{}
	formatter := &output.JsonFormatter{}
	console := mockinput.NewMockConsole()
	a := newAuthStatusAction(formatter, io.Discard, nil, flags, console)
	sa := a.(*authStatusAction)
	require.Same(t, flags, sa.flags)
	require.Same(t, formatter, sa.formatter)
}

func Test_NewAuthStatusAction(t *testing.T) {
	t.Parallel()
	action := newAuthStatusAction(
		&output.JsonFormatter{},
		&bytes.Buffer{},
		nil, // authManager
		&authStatusFlags{},
		mockinput.NewMockConsole(),
	)
	require.NotNil(t, action)
}

func Test_NewAuthStatusCmd(t *testing.T) {
	t.Parallel()
	cmd := newAuthStatusCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "status", cmd.Use)
}

func Test_NewAuthStatusFlags(t *testing.T) {
	t.Parallel()
	cmd := newAuthStatusCmd()
	global := &internal.GlobalCommandOptions{}
	flags := newAuthStatusFlags(cmd, global)
	require.NotNil(t, flags)
	assert.Equal(t, global, flags.global)
}

func Test_NewAuthStatusFlags_FC(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newAuthStatusFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewAuthStatusCmd_FC(t *testing.T) {
	t.Parallel()
	cmd := newAuthStatusCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "status")
}
