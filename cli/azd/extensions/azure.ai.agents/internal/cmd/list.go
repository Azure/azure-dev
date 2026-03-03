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

type listFlags struct {
	accountName string
	projectName string
	output      string
}

type ListAction struct {
	*AgentContext
	flags *listFlags
}

func newListCommand() *cobra.Command {
	flags := &listFlags{}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all deployed agents in the Foundry project.",
		Long: `List all deployed agents in the Foundry project.

Retrieves a list of agents from the Foundry project, showing each agent's name,
latest version, container image, and URI.`,
		Example: `  # List agents using azd environment configuration
  azd ai agent list

  # List agents with explicit account and project
  azd ai agent list --account-name myAccount --project-name myProject

  # List agents in JSON format
  azd ai agent list --output json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			setupDebugLogging(cmd.Flags())

			agentContext, err := newAgentContext(ctx, flags.accountName, flags.projectName, "", "")
			if err != nil {
				return err
			}

			action := &ListAction{
				AgentContext: agentContext,
				flags:       flags,
			}

			return action.Run(ctx)
		},
	}

	cmd.Flags().StringVarP(&flags.accountName, "account-name", "a", "", "Cognitive Services account name")
	cmd.Flags().StringVarP(&flags.projectName, "project-name", "p", "", "AI Foundry project name")
	cmd.Flags().StringVarP(&flags.output, "output", "o", "table", "Output format (json or table)")

	return cmd
}

func (a *ListAction) Run(ctx context.Context) error {
	agentClient, err := a.NewClient()
	if err != nil {
		return err
	}

	agentList, err := agentClient.ListAgents(ctx, &agent_api.ListAgentQueryParameters{}, DefaultAgentAPIVersion)
	if err != nil {
		return fmt.Errorf("failed to list agents: %w", err)
	}

	if len(agentList.Data) == 0 {
		fmt.Println("No agents found.")
		return nil
	}

	switch a.flags.output {
	case "json":
		return printAgentListJSON(agentList)
	default:
		return printAgentListTable(agentList, a.ProjectEndpoint)
	}
}

func printAgentListJSON(agentList *agent_api.AgentList) error {
	jsonBytes, err := json.MarshalIndent(agentList, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal agent list to JSON: %w", err)
	}
	fmt.Println(string(jsonBytes))
	return nil
}

func printAgentListTable(agentList *agent_api.AgentList, endpoint string) error {
	currentCtx := loadLocalContext()
	currentAgent := currentCtx.AgentName

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "\tNAME\tVERSION\tIMAGE\tURI")
	fmt.Fprintln(w, "\t----\t-------\t-----\t---")

	for _, agent := range agentList.Data {
		name := agent.Name
		if name == "" {
			name = agent.ID
		}
		latest := agent.Versions.Latest
		version := latest.Version
		if version == "" {
			version = "?"
		}

		image := extractImage(latest.Definition)
		if len(image) > 50 {
			image = "…" + image[len(image)-47:]
		}

		uri := fmt.Sprintf("%s/agents/%s", endpoint, name)

		marker := ""
		if name == currentAgent {
			marker = "→"
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", marker, name, version, image, uri)
	}

	if err := w.Flush(); err != nil {
		return err
	}

	if currentAgent != "" {
		fmt.Printf("\n→ = active agent\n")
	}
	return nil
}

// extractImage attempts to extract the image field from an agent definition.
func extractImage(definition interface{}) string {
	if definition == nil {
		return ""
	}
	// The definition may be a map[string]interface{} from JSON unmarshaling.
	switch d := definition.(type) {
	case map[string]interface{}:
		if img, ok := d["image"].(string); ok {
			return img
		}
	}
	return ""
}
