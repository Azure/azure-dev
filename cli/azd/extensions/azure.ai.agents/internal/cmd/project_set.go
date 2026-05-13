// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type projectSetResult struct {
	Endpoint     string `json:"endpoint"`
	Source       string `json:"source"`
	SourceDetail string `json:"sourceDetail"`
	SetAt        string `json:"setAt"`
}

func newProjectSetCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "set <endpoint>",
		Short: "Persist a default Foundry project endpoint.",
		Long: `Persist a default Foundry project endpoint in the azd global config
(~/.azd/config.json). Other agent commands will resolve this endpoint when no
azd environment or explicit flag is available.`,
		Example: `  # Set the default project endpoint
  azd ai agent project set https://my-project.services.ai.azure.com/api/projects/my-project`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rawEndpoint := args[0]
			outputFmt := extCtx.OutputFormat
			noPrompt := extCtx.NoPrompt

			// Validate the endpoint.
			normalized, pathWarning, err := validateProjectEndpoint(rawEndpoint)
			if err != nil {
				return err
			}

			ctx := cmd.Context()

			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			// Persist to global config.
			setAt, err := setProjectContext(ctx, azdClient, normalized)
			if err != nil {
				return err
			}

			// Warn if inside an azd project (azd env takes precedence).
			if outputFmt != "json" && !noPrompt {
				if _, envErr := azdClient.Environment().GetCurrent(
					ctx, &azdext.EmptyRequest{},
				); envErr == nil {
					fmt.Fprintln(os.Stderr,
						"warning: an active azd environment is present; "+
							"its AZURE_AI_PROJECT_ENDPOINT takes precedence "+
							"over global context.")
				}
			}

			// Warn if the endpoint path does not look like /api/projects/<project>.
			if pathWarning && outputFmt != "json" && !noPrompt {
				fmt.Fprintln(os.Stderr,
					"warning: the endpoint path does not look like /api/projects/<project>; "+
						"verify this is the correct Foundry project endpoint.")
			}

			switch outputFmt {
			case "json":
				result := projectSetResult{
					Endpoint:     normalized,
					Source:       string(SourceGlobalConfig),
					SourceDetail: "~/.azd/config.json",
					SetAt:        setAt,
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(result)
			default:
				fmt.Printf("Project endpoint set: %s\n", normalized)
				return nil
			}
		},
	}

	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"json", "table"},
		Default:       "table",
	})

	return cmd
}
