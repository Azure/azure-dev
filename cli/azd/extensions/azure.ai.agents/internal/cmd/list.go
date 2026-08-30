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
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type listFlags struct {
	output   string
	noPrompt bool
}

// newListCommand creates `azd ai agent list`. It enumerates the prompt agents
// registered on the harness configured for the azure.ai.agent service in the
// current azd project (azure.yaml).
func newListCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &listFlags{}
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List prompt agents on the harness.",
		Long: `List the prompt agents registered on the managed harness.

The target harness is derived from the azd environment (subscription, resource
group, and Foundry project). This command targets prompt agents only, meaning an
azure.ai.agent service declaring 'kind: prompt' in azure.yaml.`,
		Example: `  # List prompt agents on the configured harness
  azd ai agent list

  # List as JSON
  azd ai agent list --output json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.output = extCtx.OutputFormat
			flags.noPrompt = extCtx.NoPrompt

			ctx := azdext.WithAccessToken(cmd.Context())

			action := &ListAction{flags: flags}
			return action.Run(ctx)
		},
	}

	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"json", "table"},
		Default:       "table",
	})

	return cmd
}

// ListAction implements the prompt agent list command.
type ListAction struct {
	flags *listFlags
}

func (a *ListAction) Run(ctx context.Context) error {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return fmt.Errorf("failed to create azd client: %w", err)
	}
	defer azdClient.Close()

	pctx, isPrompt, err := resolvePromptAgentService(ctx, azdClient, "", a.flags.noPrompt)
	if err != nil {
		return err
	}
	if !isPrompt {
		return fmt.Errorf(
			"the azure.ai.agent service is not a prompt agent; " +
				"`azd ai agent list` targets services declaring `kind: prompt` in azure.yaml",
		)
	}

	client, err := pctx.newClient(ctx)
	if err != nil {
		return err
	}

	list, err := client.ListAgents(ctx, nil, pctx.Settings.EffectiveAPIVersion())
	if err != nil {
		return fmt.Errorf("failed to list prompt agents: %w", err)
	}

	switch a.flags.output {
	case "json":
		data, jsonErr := json.MarshalIndent(list, "", "  ")
		if jsonErr != nil {
			return fmt.Errorf("failed to marshal response: %w", jsonErr)
		}
		fmt.Println(string(data))
	default:
		printPromptListTable(list, pctx.Settings)
	}
	return nil
}

// printPromptListTable renders a concise table of prompt agents.
func printPromptListTable(list *agent_api.AgentList, settings *project.PromptAgentSettings) {
	if list == nil || len(list.Data) == 0 {
		fmt.Printf("No prompt agents found on %s.\n", settings.ProjectEndpoint)
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tVERSION\tSTATUS")
	for _, agent := range list.Data {
		latest := agent.Versions.Latest
		version := latest.Version
		if version == "" {
			version = "-"
		}
		status := latest.Status
		if status == "" {
			status = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", agent.Name, version, status)
	}
	_ = w.Flush()
}
