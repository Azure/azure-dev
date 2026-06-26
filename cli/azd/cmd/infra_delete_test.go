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

func Test_NewInfraDeleteAction(t *testing.T) {
	t.Parallel()
	down := &downAction{
		flags: &downFlags{},
	}
	action := newInfraDeleteAction(
		&infraDeleteFlags{},
		down,
		mockinput.NewMockConsole(),
	)
	require.NotNil(t, action)
}

func Test_NewInfraDeleteCmd(t *testing.T) {
	t.Parallel()
	cmd := newInfraDeleteCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "delete")
}

func Test_NewInfraDeleteFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newInfraDeleteFlags(cmd, global)
	require.NotNil(t, flags)
}
