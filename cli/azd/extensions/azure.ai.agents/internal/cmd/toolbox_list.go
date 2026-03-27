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

type toolboxListFlags struct {
	output string
}

func newToolboxListCommand() *cobra.Command {
	flags := &toolboxListFlags{}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all toolboxes in the Foundry project.",
		Long: `List all toolboxes in the current Azure AI Foundry project.

Displays the name, description, number of tools, and creation time
for each toolbox. Requires AZURE_AI_PROJECT_ENDPOINT in the azd environment.`,
		Example: `  # List toolboxes in table format (default)
  azd ai agent toolbox list

  # List toolboxes as JSON
  azd ai agent toolbox list --output json`,
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
			list, err := client.ListToolboxes(ctx, agent_api.ToolboxAPIVersion)
			if err != nil {
				return exterrors.ServiceFromAzure(err, exterrors.OpListToolboxes)
			}

			switch flags.output {
			case "json":
				return printToolboxListJSON(list)
			default:
				return printToolboxListTable(ctx, list)
			}
		},
	}

	cmd.Flags().StringVarP(&flags.output, "output", "o", "table", "Output format (json or table)")

	return cmd
}

func printToolboxListJSON(list *agent_api.ToolboxList) error {
	jsonBytes, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal toolbox list to JSON: %w", err)
	}
	fmt.Println(string(jsonBytes))
	return nil
}

func printToolboxListTable(_ context.Context, list *agent_api.ToolboxList) error {
	if len(list.Data) == 0 {
		fmt.Println("No toolboxes found.")
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
