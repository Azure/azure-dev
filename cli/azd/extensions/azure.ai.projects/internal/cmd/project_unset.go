// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"azure.ai.projects/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type projectUnsetFlags struct {
	outputFmt string
}

type projectUnsetResult struct {
	// PreviouslySet reports whether a project endpoint was set before this
	// unset operation. The operation succeeds (idempotently) regardless.
	PreviouslySet    bool   `json:"previouslySet"`
	PreviousEndpoint string `json:"previousEndpoint"`
}

// ProjectUnsetAction is the action for the `unset` command.
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
		return exterrors.Dependency(
			exterrors.CodeAzdClientFailed,
			"could not connect to the azd daemon",
			"ensure azd is installed and reachable; "+
				"if you are running this command outside an azd extension host, "+
				"the daemon endpoint may not be configured",
		)
	}
	defer azdClient.Close()

	previous, err := clearProjectContext(ctx, azdClient)
	if err != nil {
		return err
	}

	previouslySet := previous != ""

	switch a.flags.outputFmt {
	case "json":
		result := projectUnsetResult{
			PreviouslySet:    previouslySet,
			PreviousEndpoint: previous,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	default:
		if previouslySet {
			fmt.Println("Project endpoint cleared.")
		} else {
			fmt.Println("No project endpoint was set; nothing to clear.")
		}
		return nil
	}
}
