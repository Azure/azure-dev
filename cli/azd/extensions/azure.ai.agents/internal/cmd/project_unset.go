// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type projectUnsetFlags struct {
	outputFmt string
}

type projectUnsetResult struct {
	Cleared          bool   `json:"cleared"`
	PreviousEndpoint string `json:"previousEndpoint"`
}

// ProjectUnsetAction is the action for the `project unset` command.
type ProjectUnsetAction struct {
	flags *projectUnsetFlags
}

func newProjectUnsetCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	flags := &projectUnsetFlags{}

	cmd := &cobra.Command{
		Use:   "unset",
		Short: "Clear the persisted Foundry project endpoint.",
		Long: `Clear the persisted Foundry project endpoint from the azd global config
(~/.azd/config.json). This is idempotent — running it when no endpoint is set
is not an error.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags.outputFmt = extCtx.OutputFormat

			action := &ProjectUnsetAction{flags: flags}
			return action.Run(cmd.Context())
		},
	}

	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"json", "table"},
		Default:       "table",
	})

	return cmd
}

// Run clears the persisted project endpoint from global config.
func (a *ProjectUnsetAction) Run(ctx context.Context) error {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return fmt.Errorf("failed to create azd client: %w", err)
	}
	defer azdClient.Close()

	previous, err := clearProjectContext(ctx, azdClient)
	if err != nil {
		return err
	}

	cleared := previous != ""

	switch a.flags.outputFmt {
	case "json":
		result := projectUnsetResult{
			Cleared:          cleared,
			PreviousEndpoint: previous,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	default:
		if !cleared {
			fmt.Println("No active project endpoint to clear.")
		} else {
			fmt.Println("Project endpoint cleared.")
		}
		return nil
	}
}
