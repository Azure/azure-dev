// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type agentAddToolboxFlags struct {
	file    string
	version string
}

func newAgentAddCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <type> <name>",
		Short: "Add a capability reference to a local agent definition.",
	}
	cmd.AddCommand(newAgentAddToolboxCommand(extCtx))
	return cmd
}

func newAgentAddToolboxCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &agentAddToolboxFlags{}
	cmd := &cobra.Command{
		Use:   "toolbox <name>",
		Short: "Add a toolbox reference to agent.yaml.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := addAgentToolboxReference(flags.file, agent_yaml.ToolboxReference{
				Name: args[0], Version: flags.version,
			}); err != nil {
				return err
			}
			if extCtx.OutputFormat == "json" {
				return emitJSON(map[string]string{
					"definition": flags.file,
					"type":       "toolbox",
					"name":       args[0],
					"version":    flags.version,
				})
			}
			fmt.Printf("Added toolbox reference %q to %s.\n", args[0], flags.file)
			return nil
		},
	}
	cmd.Flags().StringVar(&flags.file, "file", "agent.yaml", "Path to the local agent definition.")
	cmd.Flags().StringVar(&flags.version, "version", "", "Optional toolbox version to pin.")
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"json", "table"}, Default: "table",
	})
	return cmd
}
