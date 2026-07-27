// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

func Test_NewMonitorAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &monitorFlags{}
	console := mockinput.NewMockConsole()
	c := &cloud.Cloud{PortalUrlBase: "https://portal.azure.com"}
	a := newMonitorAction(nil, nil, nil, nil, nil, console, flags, c, nil)
	ma := a.(*monitorAction)
	require.Same(t, flags, ma.flags)
	require.Equal(t, "https://portal.azure.com", ma.portalUrlBase)
}

func Test_NewMonitorCmd(t *testing.T) {
	t.Parallel()
	cmd := newMonitorCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "monitor", cmd.Use)
}

func Test_NewMonitorFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newMonitorFlags(cmd, global)
	require.NotNil(t, flags)
}
