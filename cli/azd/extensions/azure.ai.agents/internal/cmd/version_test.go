// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestVersionCommandMicrosoftFoundrySkillFlag(t *testing.T) {
	t.Parallel()

	cmd := newVersionCommand()
	flag := cmd.Flags().Lookup("microsoft-foundry-skill")
	require.NotNil(t, flag)
	require.True(t, flag.Hidden)
	require.NoError(t, cmd.ParseFlags([]string{"--microsoft-foundry-skill"}))
	require.Equal(t, "true", flag.Value.String())

	root := &cobra.Command{Use: "agent"}
	root.AddCommand(cmd)
	metadata := azdext.GenerateExtensionMetadata("1.0", "azure.ai.agents", root)
	require.Equal(
		t,
		[]string{"microsoft-foundry-skill"},
		extensions.ResolveCommandFlags(metadata, []string{"version", "--microsoft-foundry-skill"}),
	)
}
