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

func Test_NewPipelineConfigAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &pipelineConfigFlags{}
	console := mockinput.NewMockConsole()
	a := newPipelineConfigAction(nil, console, flags, nil, nil, nil, nil, nil, nil)
	pa := a.(*pipelineConfigAction)
	require.Same(t, flags, pa.flags)
}

func Test_NewPipelineConfigCmd(t *testing.T) {
	t.Parallel()
	cmd := newPipelineConfigCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "config", cmd.Use)
}

func Test_NewPipelineConfigFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newPipelineConfigFlags(cmd, global)
	require.NotNil(t, flags)
}
