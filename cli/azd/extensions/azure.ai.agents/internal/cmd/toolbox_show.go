// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type toolboxShowFlags struct {
	output string
}

func newToolboxShowCommand() *cobra.Command {
	flags := &toolboxShowFlags{}

	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show details of a toolbox.",
		Long: `Show details of a Foundry toolbox by name.

Displays the toolbox's properties, included tools, and the MCP endpoint URL
that can be used to connect an agent to the toolbox.`,
		Example: `  # Show toolbox details as JSON (default)
  azd ai agent toolbox show my-toolbox

  # Show toolbox details as a table
  azd ai agent toolbox show my-toolbox --output table`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			ctx := azdext.WithAccessToken(cmd.Context())
			setupDebugLogging(cmd.Flags())

			endpoint, err := resolveAgentEndpoint(ctx, "", "")
			if err != nil {
				return err
			}

			credential, err := newAgentCredential()
			if err != nil {
				return exterrors.Auth(
					exterrors.CodeCredentialCreationFailed,
					fmt.Sprintf("failed to create credential: %s", err),
					"Run 'azd auth login' to authenticate",
				)
			}

			client := agent_api.NewAgentClient(endpoint, credential)
			toolbox, err := client.GetToolbox(ctx, name, agent_api.ToolboxAPIVersion)
			if err != nil {
				return exterrors.ServiceFromAzure(err, exterrors.OpGetToolbox)
			}

			mcpEndpoint := project.ToolboxMcpEndpoint(endpoint, name)

			switch flags.output {
			case "table":
				return printToolboxShowTable(toolbox, mcpEndpoint)
			default:
				return printToolboxShowJSON(toolbox, mcpEndpoint)
			}
		},
	}

	cmd.Flags().StringVarP(&flags.output, "output", "o", "json", "Output format (json or table)")

	return cmd
}

// toolboxShowOutput wraps the toolbox object with the computed MCP endpoint for JSON output.
type toolboxShowOutput struct {
	agent_api.ToolboxObject
	MCPEndpoint string `json:"mcp_endpoint"`
}

func printToolboxShowJSON(toolbox *agent_api.ToolboxObject, mcpEndpoint string) error {
	output := toolboxShowOutput{
		ToolboxObject: *toolbox,
		MCPEndpoint:   mcpEndpoint,
	}

	jsonBytes, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal toolbox to JSON: %w", err)
	}
	fmt.Println(string(jsonBytes))
	return nil
}

func printToolboxShowTable(toolbox *agent_api.ToolboxObject, mcpEndpoint string) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "FIELD\tVALUE")
	fmt.Fprintln(w, "-----\t-----")

	fmt.Fprintf(w, "Name\t%s\n", toolbox.Name)
	fmt.Fprintf(w, "ID\t%s\n", toolbox.ID)
	if toolbox.Description != "" {
		fmt.Fprintf(w, "Description\t%s\n", toolbox.Description)
	}

	if toolbox.CreatedAt > 0 {
		fmt.Fprintf(w, "Created\t%s\n", time.Unix(toolbox.CreatedAt, 0).Format(time.RFC3339))
	}
	if toolbox.UpdatedAt > 0 {
		fmt.Fprintf(w, "Updated\t%s\n", time.Unix(toolbox.UpdatedAt, 0).Format(time.RFC3339))
	}

	fmt.Fprintf(w, "Tools\t%d\n", len(toolbox.Tools))
	for i, raw := range toolbox.Tools {
		toolType, toolName := agent_api.ToolSummary(raw)
		label := toolType
		if toolName != "" {
			label = fmt.Sprintf("%s (%s)", toolType, toolName)
		}
		fmt.Fprintf(w, "  Tool %d\t%s\n", i+1, label)
	}

	fmt.Fprintf(w, "MCP Endpoint\t%s\n", mcpEndpoint)

	return w.Flush()
}
