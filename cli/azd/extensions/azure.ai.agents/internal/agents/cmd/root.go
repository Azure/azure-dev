// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

func NewAgentRootCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "agent <command> [options]",
		Short: fmt.Sprintf("Ship agents with Microsoft Foundry from your terminal. %s", color.YellowString("(Preview)")),
	}

	cmd.AddCommand(azdext.NewListenCommand(configureExtensionHost))
	cmd.AddCommand(newVersionCommand())
	cmd.AddCommand(azdext.NewMetadataCommand("1.0", "azure.ai.agents", func() *cobra.Command {
		return cmd
	}))

	cmd.AddCommand(newInitCommand(extCtx))
	cmd.AddCommand(newRunCommand(extCtx))
	cmd.AddCommand(newInvokeCommand(extCtx))
	cmd.AddCommand(newMcpCommand())
	cmd.AddCommand(newShowCommand(extCtx))
	cmd.AddCommand(newMonitorCommand(extCtx))
	cmd.AddCommand(newFilesCommand(extCtx))
	cmd.AddCommand(newSessionCommand(extCtx))

	return cmd
}
