// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type showFlags struct {
	name   string
	output string
}

// ShowAction handles the execution of the show command.
type ShowAction struct {
	*AgentContext
	flags     *showFlags
	azdClient *azdext.AzdClient
}

func newShowCommand() *cobra.Command {
	flags := &showFlags{}

	cmd := &cobra.Command{
		Use:   "show [name]",
		Short: "Show the status of a hosted agent.",
		Long: `Show the status of a hosted agent.

The agent name and version are resolved automatically from the azure.yaml service
configuration and the current azd environment. Optionally specify the service name
(from azure.yaml) as a positional argument when multiple agent services exist.`,
		Example: `  # Show status (auto-resolves from azure.yaml)
  azd ai agent show

  # Show status for a specific agent service
  azd ai agent show my-agent

  # Show status in table format
  azd ai agent show --output table`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				flags.name = args[0]
			}
			ctx := azdext.WithAccessToken(cmd.Context())
			logCleanup := setupDebugLogging(cmd.Flags())
			defer logCleanup()

			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			info, err := resolveAgentServiceFromProject(ctx, azdClient, flags.name, rootFlags.NoPrompt)
			if err != nil {
				return err
			}

			if info.AgentName == "" {
				return fmt.Errorf(
					"agent name could not be resolved from azd environment for service '%s'\n\n"+
						"Run 'azd deploy' first to deploy the agent, or check your azd environment values",
					info.ServiceName,
				)
			}
			if info.Version == "" {
				return fmt.Errorf(
					"agent version could not be resolved from azd environment for service '%s'\n\n"+
						"Run 'azd deploy' first to deploy the agent, or check your azd environment values",
					info.ServiceName,
				)
			}

			agentContext, err := newAgentContext(ctx, "", "", info.AgentName, info.Version)
			if err != nil {
				return err
			}

			action := &ShowAction{
				AgentContext: agentContext,
				flags:        flags,
				azdClient:    azdClient,
			}

			return action.Run(ctx)
		},
	}

	cmd.Flags().StringVarP(&flags.output, "output", "o", "json", "Output format (json or table)")

	return cmd
}

// Run executes the show command logic.
func (a *ShowAction) Run(ctx context.Context) error {
	agentClient, err := a.NewClient()
	if err != nil {
		return err
	}

	if isVNextEnabled(ctx, a.azdClient) {
		version, err := agentClient.GetAgentVersion(
			ctx, a.Name, a.Version, DefaultAgentAPIVersion,
		)
		if err != nil {
			return fmt.Errorf("failed to get agent version: %w", err)
		}

		switch a.flags.output {
		case "table":
			return printAgentVersionTable(version)
		default:
			return printAgentVersionJSON(version)
		}
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

func printAgentVersionJSON(version *agent_api.AgentVersionObject) error {
	jsonBytes, err := json.MarshalIndent(version, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal agent version to JSON: %w", err)
	}
	fmt.Println(string(jsonBytes))
	return nil
}

func printAgentVersionTable(version *agent_api.AgentVersionObject) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "FIELD\tVALUE")
	fmt.Fprintln(w, "-----\t-----")

	fmt.Fprintf(w, "ID\t%s\n", version.ID)
	fmt.Fprintf(w, "Name\t%s\n", version.Name)
	fmt.Fprintf(w, "Version\t%s\n", version.Version)
	if version.Description != nil {
		fmt.Fprintf(w, "Description\t%s\n", *version.Description)
	}
	if version.CreatedAt != 0 {
		ts := time.Unix(version.CreatedAt, 0).UTC().Format(time.RFC3339)
		fmt.Fprintf(w, "Created At\t%s\n", ts)
	}
	for k, v := range version.Metadata {
		fmt.Fprintf(w, "Metadata[%s]\t%s\n", k, v)
	}

	return w.Flush()
}
