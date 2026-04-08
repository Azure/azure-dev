// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package cmd provides the CLI commands for the azd exec extension.
package cmd

import (
	"errors"
	"fmt"
	"os"

	"microsoft.azd.exec/internal/executor"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

var (
	// Populated at build time via ldflags.
	Version = "dev"
)

// NewRootCommand creates and configures the root cobra command for azd exec.
func NewRootCommand() *cobra.Command {
	var (
		shell       string
		interactive bool
	)

	rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name:    "exec",
		Version: Version,
		Use:     "exec [command] [args...] | [script-file] [-- script-args...]",
		Short:   "Exec - Execute commands/scripts with Azure Developer CLI context",
		Long: `Exec is an Azure Developer CLI extension that executes commands and scripts
with full access to azd environment variables and configuration.

Commands are run with the azd environment loaded into the child process.
Multiple arguments use direct process execution (no shell wrapping).
A single quoted argument uses shell inline execution.

Examples:
	azd exec python script.py                     # Direct exec (exact argv)
	azd exec npm run dev                           # Direct exec (no shell)
	azd exec -- python app.py --port 8000          # Direct exec with flags
	azd exec 'echo $AZURE_ENV_NAME'               # Inline via shell (Linux/macOS)
	azd exec ./setup.sh                            # Execute script file
	azd exec --shell pwsh "Write-Host 'Hello'"     # Inline PowerShell
	azd exec ./build.sh -- --verbose               # Script with args
	azd exec -i ./init.sh                          # Interactive mode`,
	})

	rootCmd.Args = cobra.MinimumNArgs(1)
	rootCmd.RunE = func(cmd *cobra.Command, args []string) error {
		scriptInput := args[0]

		var scriptArgs []string
		if len(args) > 1 {
			scriptArgs = args[1:]
		}

		exec, err := executor.New(executor.Config{
			Shell:       shell,
			Interactive: interactive,
			Args:        scriptArgs,
		})
		if err != nil {
			return fmt.Errorf("invalid configuration: %w", err)
		}

		// Try file execution first; fall back based on argument shape.
		if err := exec.Execute(cmd.Context(), scriptInput); err != nil {
			if _, ok := errors.AsType[*executor.ScriptNotFoundError](err); ok {
				// Multiple args + no explicit shell → direct process exec (exact argv).
				// Single arg or explicit shell → shell inline execution.
				if len(scriptArgs) > 0 && shell == "" {
					return exec.ExecuteDirect(cmd.Context(), scriptInput, scriptArgs)
				}
				return exec.ExecuteInline(cmd.Context(), scriptInput)
			}
			return err
		}
		return nil
	}

	sdkPreRunE := rootCmd.PersistentPreRunE
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if sdkPreRunE != nil {
			if err := sdkPreRunE(cmd, args); err != nil {
				return err
			}
		}

		if extCtx.Debug {
			_ = os.Setenv("AZD_DEBUG", "true")
		}
		if extCtx.NoPrompt {
			_ = os.Setenv("AZD_NO_PROMPT", "true")
		}

		return nil
	}

	rootCmd.FParseErrWhitelist.UnknownFlags = true
	rootCmd.Flags().SetInterspersed(false)
	rootCmd.PersistentFlags().SetInterspersed(false)

	rootCmd.Flags().StringVarP(&shell, "shell", "s", "",
		"Shell to use for execution (bash, sh, zsh, pwsh, powershell, cmd). Auto-detected if not specified.")
	rootCmd.Flags().BoolVarP(&interactive, "interactive", "i", false,
		"Run script in interactive mode")

	rootCmd.AddCommand(
		azdext.NewVersionCommand("microsoft.azd.exec", Version, nil),
		azdext.NewListenCommand(nil),
		azdext.NewMetadataCommand("1.0", "microsoft.azd.exec", NewRootCommand),
	)

	return rootCmd
}
