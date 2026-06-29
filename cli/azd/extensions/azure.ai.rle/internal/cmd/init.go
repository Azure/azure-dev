// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type rleInitFlags struct {
	manifest string
	force    bool
}

func newInitCommand() *cobra.Command {
	flags := &rleInitFlags{}

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold a local RLE environment",
		Args:  cobra.NoArgs,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if initUsedLongManifestFlag(os.Args[1:]) {
				return &azdext.LocalError{
					Message:    "--manifest is not supported.",
					Code:       "rle_manifest_long_flag_unsupported",
					Category:   azdext.LocalErrorCategoryUser,
					Suggestion: "Use -m <manifest>.",
				}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var manifestBytes []byte
			if flags.manifest != "" {
				var err error
				manifestBytes, err = readManifestContent(cmd.Context(), flags.manifest)
				if err != nil {
					return err
				}
			} else {
				manifestBytes = []byte(defaultEchoManifest)
			}

			manifest, err := parseRleManifest(manifestBytes)
			if err != nil {
				return err
			}
			manifestState, err := stateFromManifest(manifest)
			if err != nil {
				return err
			}
			envName := manifestState.Name
			if envName == "" {
				return &azdext.LocalError{
					Message:    "RLE manifest name is required.",
					Code:       "rle_environment_name_required",
					Category:   azdext.LocalErrorCategoryUser,
					Suggestion: "Set name or template.name in the manifest.",
				}
			}
			envName, err = validateEnvName(envName)
			if err != nil {
				return &azdext.LocalError{
					Message:    err.Error(),
					Code:       "rle_invalid_environment_name",
					Category:   azdext.LocalErrorCategoryUser,
					Suggestion: "Use snake_case starting with a letter, for example code_rl.",
				}
			}

			sessionDir, err := scaffoldRleSession(
				envName,
				".",
				manifestState.LocalImage,
				manifestState.Image,
				flags.force,
			)
			if err != nil {
				return err
			}
			if err := writeManifestToSession(manifestBytes, sessionDir); err != nil {
				return err
			}

			if err := saveRleStateIn(sessionDir, manifestState); err != nil {
				return err
			}

			displayDir, err := filepath.Abs(sessionDir)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(
				cmd.OutOrStdout(),
				"Created OpenEnv-style environment at: %s\n"+
					"Next steps:\n"+
					"  cd %s\n"+
					"  azd ai rle run\n"+
					"  azd ai rle invoke --local\n"+
					"  azd ai rle deploy --project-id <project-id>\n",
				displayDir,
				displayDir,
			)
			return err
		},
	}

	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		var help strings.Builder
		help.WriteString("Scaffold a local RLE environment\n")
		help.WriteString("Usage:\n")
		help.WriteString("  rle init [flags]\n")
		help.WriteString("Flags:\n")
		help.WriteString("      --force     Overwrite generated files in an existing non-empty session directory\n")
		help.WriteString("  -h, --help      help for init\n")
		help.WriteString("  -m string       Path or HTTPS URL to an RLE manifest to copy into the session.\n")
		if cmd.InheritedFlags().HasAvailableFlags() {
			help.WriteString("Global Flags:\n")
			help.WriteString(cmd.InheritedFlags().FlagUsages())
		}
		_, _ = fmt.Fprint(cmd.OutOrStdout(), help.String())
	})
	cmd.Flags().StringVarP(
		&flags.manifest,
		"manifest",
		"m",
		"",
		"Path or HTTPS URL to an RLE manifest to copy into the session.",
	)
	_ = cmd.Flags().MarkHidden("manifest")
	cmd.Flags().BoolVar(&flags.force, "force", false, "Overwrite generated files in an existing non-empty session directory")
	return cmd
}

func initUsedLongManifestFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--manifest" || strings.HasPrefix(arg, "--manifest=") {
			return true
		}
	}
	return false
}

func writeManifestToSession(data []byte, sessionDir string) error {
	return os.WriteFile(filepath.Join(sessionDir, rleManifestFile), data, 0600)
}
