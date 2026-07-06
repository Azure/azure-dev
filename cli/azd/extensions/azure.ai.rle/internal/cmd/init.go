// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type rleInitFlags struct {
	force bool
}

func newInitCommand() *cobra.Command {
	flags := &rleInitFlags{}

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a local RLE environment",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			envNameOverride := ""
			if len(args) == 1 {
				envNameOverride = args[0]
			}
			envName := firstNonEmpty(envNameOverride, "echo_env")
			var err error
			envName, err = validateEnvName(envName)
			if err != nil {
				return &azdext.LocalError{
					Message:    err.Error(),
					Code:       "rle_invalid_environment_name",
					Category:   azdext.LocalErrorCategoryUser,
					Suggestion: "Use snake_case starting with a letter, for example code_rl.",
				}
			}

			sessionDir, err := checkoutOpenEnvEchoSampleFunc(envName, ".", flags.force)
			if err != nil {
				return err
			}
			if err := saveRleStateIn(sessionDir, defaultRleState(envName)); err != nil {
				return err
			}

			displayDir := "." + string(os.PathSeparator) + sessionDir
			_, err = fmt.Fprintf(
				cmd.OutOrStdout(),
				"Created OpenEnv-style environment at: %s\n"+
					"Next steps:\n"+
					"  cd \"%s\"\n"+
					"  azd ai rle run\n"+
					"  $env:FOUNDRY_PROJECT_ENDPOINT = \"https://<account>.services.ai.azure.com/api/projects/<project>\"\n"+
					"  $env:AZURE_CONTAINER_REGISTRY_ENDPOINT = \"<registry>.azurecr.io\"\n"+
					"  azd ai rle deploy\n",
				displayDir,
				displayDir,
			)
			return err
		},
	}

	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		var help strings.Builder
		help.WriteString("Initialize a local RLE environment\n")
		help.WriteString("Usage:\n")
		help.WriteString("  rle init [environment-name] [flags]\n")
		help.WriteString("Flags:\n")
		help.WriteString("      --force     Overwrite generated files in an existing non-empty session directory\n")
		help.WriteString("  -h, --help      help for init\n")
		if cmd.InheritedFlags().HasAvailableFlags() {
			help.WriteString("Global Flags:\n")
			help.WriteString(cmd.InheritedFlags().FlagUsages())
		}
		_, _ = fmt.Fprint(cmd.OutOrStdout(), help.String())
	})
	cmd.Flags().BoolVar(&flags.force, "force", false, "Overwrite generated files in an existing non-empty session directory")
	return cmd
}
