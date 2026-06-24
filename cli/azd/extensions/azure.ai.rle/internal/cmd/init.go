// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type rleInitFlags struct {
	path  string
	image string
	force bool
}

func newInitCommand() *cobra.Command {
	flags := &rleInitFlags{}

	cmd := &cobra.Command{
		Use:   "init <env-name>",
		Short: "Scaffold a local RLE environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			envName, err := validateEnvName(args[0])
			if err != nil {
				return &azdext.LocalError{
					Message:    err.Error(),
					Code:       "rle_invalid_environment_name",
					Category:   azdext.LocalErrorCategoryUser,
					Suggestion: "Use snake_case starting with a letter, for example code_rl.",
				}
			}

			sessionDir, err := scaffoldRleSession(envName, flags.path, flags.image, flags.force)
			if err != nil {
				return err
			}

			state := defaultRleState(envName, defaultRecipeName)
			if err := saveRleStateIn(sessionDir, state); err != nil {
				return err
			}
			if _, err := materializeBundledRleSdk(sessionDir); err != nil {
				return err
			}

			displayDir, err := filepath.Abs(sessionDir)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(
				cmd.OutOrStdout(),
				"Created OpenEnv-style environment at: %s\nNext steps:\n  cd %s\n  azd ai rle deploy\n",
				displayDir,
				displayDir,
			)
			return err
		},
	}

	cmd.Flags().StringVar(&flags.path, "path", ".", "Directory where the RLE session folder is created")
	cmd.Flags().StringVar(&flags.image, "image", "",
		"Image reference written to rle.yaml (defaults to one derived from the environment name at deploy time)")
	cmd.Flags().BoolVar(&flags.force, "force", false, "Overwrite local RLE state if it already exists")
	return cmd
}
