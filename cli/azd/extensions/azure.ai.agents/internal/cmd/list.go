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

type listFlags struct {
	output string
}

func newListCommand() *cobra.Command {
	flags := &listFlags{}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List agent services in the project.",
		Long: `List agent services defined in azure.yaml.

Shows each azure.ai.agent service with its name and relative path.
The currently active agent (set via AZD_AI_AGENT_INVOKE_NAME) is marked with an arrow.`,
		Example: `  # List agents in the project
  azd ai agent list

  # List agents in JSON format
  azd ai agent list --output json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			setupDebugLogging(cmd.Flags())

			return runList(ctx, flags)
		},
	}

	cmd.Flags().StringVarP(&flags.output, "output", "o", "table", "Output format (json or table)")

	return cmd
}

// agentServiceEntry represents an agent service for display.
type agentServiceEntry struct {
	Name         string `json:"name"`
	RelativePath string `json:"relativePath"`
	Active       bool   `json:"active"`
}

func runList(ctx context.Context, flags *listFlags) error {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return fmt.Errorf("failed to create azd client: %w", err)
	}
	defer azdClient.Close()

	projectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil || projectResponse.Project == nil {
		return fmt.Errorf("failed to read azure.yaml: %w", err)
	}

	activeAgent := getInvokeEnvValue(ctx, EnvKeyAgentInvokeName)

	var entries []agentServiceEntry
	for _, svc := range projectResponse.Project.Services {
		if svc.Host != AiAgentHost {
			continue
		}
		entries = append(entries, agentServiceEntry{
			Name:         svc.Name,
			RelativePath: svc.RelativePath,
			Active:       svc.Name == activeAgent,
		})
	}

	if len(entries) == 0 {
		fmt.Println("No agent services found in azure.yaml.")
		return nil
	}

	switch flags.output {
	case "json":
		jsonBytes, err := json.MarshalIndent(entries, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal to JSON: %w", err)
		}
		fmt.Println(string(jsonBytes))
	default:
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "\tNAME\tPATH")
		fmt.Fprintln(w, "\t----\t----")

		for _, e := range entries {
			marker := ""
			if e.Active {
				marker = "→"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", marker, e.Name, e.RelativePath)
		}

		if err := w.Flush(); err != nil {
			return err
		}

		if activeAgent != "" {
			fmt.Printf("\n→ = active invoke target (%s)\n", EnvKeyAgentInvokeName)
		}
	}

	return nil
}
