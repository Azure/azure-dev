// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/llm/tools"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func newMcpFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *mcpFlags {
	flags := &mcpFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newMcpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run MCP server.",
	}
}

type mcpFlags struct {
	global *internal.GlobalCommandOptions
	internal.EnvFlag
}

func (i *mcpFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	i.EnvFlag.Bind(local, global)
	i.global = global
}

type mcpAction struct {
	console             input.Console
	cmdRun              exec.CommandRunner
	flags               *mcpFlags
	alphaFeatureManager *alpha.FeatureManager
}

func newMcpAction(
	cmdRun exec.CommandRunner,
	console input.Console,
	flags *mcpFlags,
	alphaFeatureManager *alpha.FeatureManager,
) actions.Action {
	return &mcpAction{
		console:             console,
		cmdRun:              cmdRun,
		flags:               flags,
		alphaFeatureManager: alphaFeatureManager,
	}
}

func (i *mcpAction) Run(ctx context.Context) (*actions.ActionResult, error) {

	// Create a new MCP server
	s := server.NewMCPServer(
		"AZD MCP Server 🚀", "1.0.0",
		server.WithToolCapabilities(false),
	)
	s.EnableSampling()

	s.AddTools(
		tools.NewHello(),
	)

	// Start the server using stdio transport
	if err := server.ServeStdio(s); err != nil {
		fmt.Printf("Server error: %v\n", err)
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{},
	}, nil
}

func getCmdMcpHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription("Starts the azd MCP server.",
		[]string{})
}

func getCmdMcpHelpFooter(*cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{})
}
