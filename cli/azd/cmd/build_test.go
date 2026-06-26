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

func Test_NewBuildAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &buildFlags{}
	args := []string{"svc"}
	console := mockinput.NewMockConsole()
	formatter := &output.JsonFormatter{}
	a := newBuildAction(
		flags, args, nil, nil, nil, nil, console, formatter, io.Discard, nil,
	)
	ba := a.(*buildAction)
	require.Same(t, flags, ba.flags)
	require.Equal(t, args, ba.args)
}

func Test_NewBuildCmd(t *testing.T) {
	t.Parallel()
	cmd := newBuildCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "build <service>", cmd.Use)
}

func Test_NewBuildFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newBuildFlags(cmd, global)
	require.NotNil(t, flags)
}
