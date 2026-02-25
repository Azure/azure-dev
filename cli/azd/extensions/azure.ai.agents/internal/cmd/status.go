// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type statusFlags struct {
	accountName string
	projectName string
	name        string
	version     string
	output      string
}

// StatusAction handles the execution of the status command.
type StatusAction struct {
	*AgentContext
	flags *statusFlags
}

func newStatusCommand() *cobra.Command {
	flags := &statusFlags{}

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Get the status of a hosted agent deployment.",
		Long: `Get the status of a hosted agent deployment.

Retrieves the runtime status of a hosted agent container, including its current state,
replica configuration, and any error messages.`,
		Example: `  # Get status using azd environment configuration
  azd ai agent status --name my-agent --version 1

  # Get status with explicit account and project
  azd ai agent status --name my-agent --version 1 --account-name myAccount --project-name myProject

  # Get status in table format
  azd ai agent status --name my-agent --version 1 --output table`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			setupDebugLogging(cmd.Flags())

			agentContext, err := newAgentContext(ctx, flags.accountName, flags.projectName, flags.name, flags.version)
			if err != nil {
				return err
			}

			action := &StatusAction{
				AgentContext: agentContext,
				flags:       flags,
			}

			return action.Run(ctx)
		},
	}

	cmd.Flags().StringVarP(&flags.accountName, "account-name", "a", "", "Cognitive Services account name")
	cmd.Flags().StringVarP(&flags.projectName, "project-name", "p", "", "AI Foundry project name")
	cmd.Flags().StringVarP(&flags.name, "name", "n", "", "Name of the hosted agent (required)")
	cmd.Flags().StringVarP(&flags.version, "version", "v", "", "Version of the hosted agent (required)")
	cmd.Flags().StringVarP(&flags.output, "output", "o", "json", "Output format (json or table)")

	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("version")

	return cmd
}

// Run executes the status command logic.
func (a *StatusAction) Run(ctx context.Context) error {
	agentClient, err := a.NewClient()
	if err != nil {
		return err
	}

	container, err := agentClient.GetAgentContainer(ctx, a.Name, a.Version, DefaultAgentAPIVersion)
	if err != nil {
		return fmt.Errorf("failed to get agent container status: %w", err)
	}

	switch a.flags.output {
	case "table":
		return printStatusTable(container)
	default:
		return printStatusJSON(container)
	}
}

func printStatusJSON(container interface{}) error {
	jsonBytes, err := json.MarshalIndent(container, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal status to JSON: %w", err)
	}
	fmt.Println(string(jsonBytes))
	return nil
}

func printStatusTable(container interface{}) error {
	// Marshal to generic map for flexible field access
	jsonBytes, err := json.Marshal(container)
	if err != nil {
		return fmt.Errorf("failed to marshal status: %w", err)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &data); err != nil {
		return fmt.Errorf("failed to parse status: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "FIELD\tVALUE")
	fmt.Fprintln(w, "-----\t-----")

	printField(w, data, "status", "Status")
	printField(w, data, "min_replicas", "Min Replicas")
	printField(w, data, "max_replicas", "Max Replicas")
	printField(w, data, "created_at", "Created At")
	printField(w, data, "updated_at", "Updated At")
	printField(w, data, "error_message", "Error Message")

	return w.Flush()
}

func printField(w *tabwriter.Writer, data map[string]interface{}, key, label string) {
	if val, ok := data[key]; ok && val != nil {
		fmt.Fprintf(w, "%s\t%v\n", label, val)
	}
}
