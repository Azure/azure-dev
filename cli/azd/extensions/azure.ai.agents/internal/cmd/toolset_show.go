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

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type toolsetShowFlags struct {
	output string
}

func newToolsetShowCommand() *cobra.Command {
	flags := &toolsetShowFlags{}

	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show details of a toolset.",
		Long: `Show details of a Foundry toolset by name.

Displays the toolset's properties, included tools, and the MCP endpoint URL
that can be used to connect an agent to the toolset.`,
		Example: `  # Show toolset details as JSON (default)
  azd ai agent toolset show my-toolset

  # Show toolset details as a table
  azd ai agent toolset show my-toolset --output table`,
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
			toolset, err := client.GetToolset(ctx, name, agent_api.ToolsetAPIVersion)
			if err != nil {
				return exterrors.ServiceFromAzure(err, exterrors.OpGetToolset)
			}

			mcpEndpoint := fmt.Sprintf("%s/toolsets/%s/mcp", endpoint, name)

			switch flags.output {
			case "table":
				return printToolsetShowTable(toolset, mcpEndpoint)
			default:
				return printToolsetShowJSON(toolset, mcpEndpoint)
			}
		},
	}

	cmd.Flags().StringVarP(&flags.output, "output", "o", "json", "Output format (json or table)")

	return cmd
}

// toolsetShowOutput wraps the toolset object with the computed MCP endpoint for JSON output.
type toolsetShowOutput struct {
	agent_api.ToolsetObject
	MCPEndpoint string `json:"mcp_endpoint"`
}

func printToolsetShowJSON(toolset *agent_api.ToolsetObject, mcpEndpoint string) error {
	output := toolsetShowOutput{
		ToolsetObject: *toolset,
		MCPEndpoint:   mcpEndpoint,
	}

	jsonBytes, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal toolset to JSON: %w", err)
	}
	fmt.Println(string(jsonBytes))
	return nil
}

func printToolsetShowTable(toolset *agent_api.ToolsetObject, mcpEndpoint string) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "FIELD\tVALUE")
	fmt.Fprintln(w, "-----\t-----")

	fmt.Fprintf(w, "Name\t%s\n", toolset.Name)
	fmt.Fprintf(w, "ID\t%s\n", toolset.ID)
	if toolset.Description != "" {
		fmt.Fprintf(w, "Description\t%s\n", toolset.Description)
	}

	if toolset.CreatedAt > 0 {
		fmt.Fprintf(w, "Created\t%s\n", time.Unix(toolset.CreatedAt, 0).Format(time.RFC3339))
	}
	if toolset.UpdatedAt > 0 {
		fmt.Fprintf(w, "Updated\t%s\n", time.Unix(toolset.UpdatedAt, 0).Format(time.RFC3339))
	}

	fmt.Fprintf(w, "Tools\t%d\n", len(toolset.Tools))
	for i, raw := range toolset.Tools {
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
