// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

func Test_NewUpAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &upFlags{}
	console := mockinput.NewMockConsole()
	a := newUpAction(flags, console, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	ua := a.(*upAction)
	require.Same(t, flags, ua.flags)
}

func Test_NewUpCmd(t *testing.T) {
	t.Parallel()
	cmd := newUpCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "up", cmd.Use)
}

func Test_NewUpFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newUpFlags(cmd, global)
	require.NotNil(t, flags)
}
