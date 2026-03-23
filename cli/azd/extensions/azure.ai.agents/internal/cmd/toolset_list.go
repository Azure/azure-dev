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

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type toolsetListFlags struct {
	output string
}

func newToolsetListCommand() *cobra.Command {
	flags := &toolsetListFlags{}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all toolsets in the Foundry project.",
		Long: `List all toolsets in the current Azure AI Foundry project.

Displays the name, description, number of tools, and creation time
for each toolset. Requires AZURE_AI_PROJECT_ENDPOINT in the azd environment.`,
		Example: `  # List toolsets in table format (default)
  azd ai agent toolset list

  # List toolsets as JSON
  azd ai agent toolset list --output json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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
			list, err := client.ListToolsets(ctx, agent_api.ToolsetAPIVersion)
			if err != nil {
				return exterrors.ServiceFromAzure(err, exterrors.OpListToolsets)
			}

			switch flags.output {
			case "json":
				return printToolsetListJSON(list)
			default:
				return printToolsetListTable(ctx, list)
			}
		},
	}

	cmd.Flags().StringVarP(&flags.output, "output", "o", "table", "Output format (json or table)")

	return cmd
}

func printToolsetListJSON(list *agent_api.ToolsetList) error {
	jsonBytes, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal toolset list to JSON: %w", err)
	}
	fmt.Println(string(jsonBytes))
	return nil
}

func printToolsetListTable(_ context.Context, list *agent_api.ToolsetList) error {
	if len(list.Data) == 0 {
		fmt.Println("No toolsets found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tDESCRIPTION\tTOOLS\tCREATED")
	fmt.Fprintln(w, "----\t-----------\t-----\t-------")

	for _, ts := range list.Data {
		desc := ts.Description
		if len(desc) > 50 {
			desc = desc[:47] + "..."
		}

		created := ""
		if ts.CreatedAt > 0 {
			created = time.Unix(ts.CreatedAt, 0).Format(time.RFC3339)
		}

		fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", ts.Name, desc, len(ts.Tools), created)
	}

	return w.Flush()
}
