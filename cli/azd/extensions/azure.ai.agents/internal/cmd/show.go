// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type showFlags struct {
	accountName string
	projectName string
	name        string
	version     string
	output      string
}

// ShowAction handles the execution of the show command.
type ShowAction struct {
	*AgentContext
	flags *showFlags
}

func newShowCommand() *cobra.Command {
	flags := &showFlags{}

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the status of a hosted agent deployment.",
		Long: `Show the status of a hosted agent deployment.

Retrieves the runtime status of a hosted agent container, including its current state,
replica configuration, and any error messages.`,
		Example: `  # Show status using azd environment configuration
  azd ai agent show --name my-agent --version 1

  # Show status with explicit account and project
  azd ai agent show --name my-agent --version 1 --account-name myAccount --project-name myProject

  # Show status in table format
  azd ai agent show --name my-agent --version 1 --output table`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			setupDebugLogging(cmd.Flags())

			agentContext, err := newAgentContext(ctx, flags.accountName, flags.projectName, flags.name, flags.version)
			if err != nil {
				return err
			}

			action := &ShowAction{
				AgentContext: agentContext,
				flags:        flags,
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

// Run executes the show command logic.
func (a *ShowAction) Run(ctx context.Context) error {
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

func printStatusJSON(container *agent_api.AgentContainerObject) error {
	jsonBytes, err := json.MarshalIndent(container, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal status to JSON: %w", err)
	}
	fmt.Println(string(jsonBytes))
	return nil
}

func printStatusTable(container *agent_api.AgentContainerObject) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "FIELD\tVALUE")
	fmt.Fprintln(w, "-----\t-----")

	fmt.Fprintf(w, "ID\t%s\n", container.ID)
	fmt.Fprintf(w, "Status\t%s\n", container.Status)
	if container.MinReplicas != nil {
		fmt.Fprintf(w, "Min Replicas\t%d\n", *container.MinReplicas)
	}
	if container.MaxReplicas != nil {
		fmt.Fprintf(w, "Max Replicas\t%d\n", *container.MaxReplicas)
	}
	fmt.Fprintf(w, "Created At\t%s\n", container.CreatedAt)
	fmt.Fprintf(w, "Updated At\t%s\n", container.UpdatedAt)
	if container.ErrorMessage != nil {
		fmt.Fprintf(w, "Error Message\t%s\n", *container.ErrorMessage)
	}

	if container.Container != nil {
		c := container.Container
		fmt.Fprintf(w, "Health State\t%s\n", c.HealthState)
		fmt.Fprintf(w, "Provisioning State\t%s\n", c.ProvisioningState)
		fmt.Fprintf(w, "Container State\t%s\n", c.State)
		fmt.Fprintf(w, "Container Updated On\t%s\n", c.UpdatedOn)
		for i, r := range c.Replicas {
			fmt.Fprintf(w, "Replica %d Name\t%s\n", i+1, r.Name)
			fmt.Fprintf(w, "Replica %d State\t%s\n", i+1, r.State)
			if r.ContainerState != "" {
				fmt.Fprintf(w, "Replica %d Container State\t%s\n", i+1, r.ContainerState)
			}
		}
	}

	return w.Flush()
}
