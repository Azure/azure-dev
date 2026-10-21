// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/featureflags"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newListAlphaCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	cmd := &cobra.Command{
		Use:   "list-alpha",
		Short: "Display all available agent preview features and their status.",
		Long: `Display all declared agent preview features, including disabled features,
sorted by name. Each entry includes its name, description, and On or Off status,
matching the default format of 'azd config list-alpha'.

Status uses the same opt-in checks as agent operations: user config, the process
environment, the selected azd environment (-e), and every azure.yaml service's
env mapping. Any enabling source turns a feature on; false does not override
another source's opt-in.

Without a project or default environment, the available sources still apply.
An invalid explicit environment or a source read error fails without printing
feature records. This command is always available and needs no preview opt-in.`,
		Example: fmt.Sprintf(`  # List every agent preview feature and its effective status
  azd ai agent list-alpha

  # Read values from a specific environment
  azd ai agent list-alpha --environment dev

  # Enable voice through user config
  azd config set alpha.%s on

  # Alternatively, enable voice in the selected environment
  azd env set %s true`, featureflags.Voice.ID, featureflags.Voice.EnvVar),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := azdext.NewAzdClient()
			if err != nil {
				return exterrors.Internal(
					exterrors.CodeAzdClientFailed,
					fmt.Sprintf("connecting to azd to list preview features: %s", err),
				)
			}
			defer client.Close()

			features := featureflags.List()
			records := make([]string, 0, len(features))
			for _, feature := range features {
				enabled, err := featureflags.IsEnabled(cmd.Context(), client, feature, extCtx.Environment)
				if err != nil {
					return err
				}
				status := "Off"
				if enabled {
					status = "On"
				}
				records = append(records, fmt.Sprintf(
					"Name: %s\nDescription: %s\nStatus: %s", feature.ID, feature.Description, status,
				))
			}

			if _, err := fmt.Fprintln(cmd.OutOrStdout(), strings.Join(records, "\n\n")); err != nil {
				return fmt.Errorf("writing preview feature list: %w", err)
			}
			return nil
		},
	}
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"default"},
		Default:       "default",
	})
	return cmd
}
