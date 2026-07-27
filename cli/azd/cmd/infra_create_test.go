// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal"
	internalcmd "github.com/azure/azure-dev/cli/azd/internal/cmd"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

func Test_NewInfraCreateAction(t *testing.T) {
	t.Parallel()
	provision := &internalcmd.ProvisionAction{}
	action := newInfraCreateAction(
		&infraCreateFlags{},
		provision,
		mockinput.NewMockConsole(),
	)
	require.NotNil(t, action)
}

func Test_NewInfraCreateCmd(t *testing.T) {
	t.Parallel()
	cmd := newInfraCreateCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "create")
}

func Test_NewInfraCreateFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newInfraCreateFlags(cmd, global)
	require.NotNil(t, flags)
}
