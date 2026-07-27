// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal"
)

func Test_NewMcpStartAction(t *testing.T) {
	t.Parallel()
	action := newMcpStartAction(
		&mcpStartFlags{},
		nil, // userConfigManager
		nil, // extensionManager
		nil, // grpcServer
	)
	require.NotNil(t, action)
}

func Test_NewMcpStartFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newMcpStartFlags(cmd, global)
	require.NotNil(t, flags)
}
