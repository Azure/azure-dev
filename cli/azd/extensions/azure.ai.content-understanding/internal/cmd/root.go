// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	return newRootCommand(openServiceSession)
}

func newRootCommand(open sessionFactory) *cobra.Command {
	return newRootCommandWithInfra(open, openInfraHost)
}

func newRootCommandWithInfra(open sessionFactory, infraOpen infraHostFactory) *cobra.Command {
	rootCmd, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name:    "cu",
		Use:     "cu <command> [options]",
		Version: Version,
		Short:   "Work with Azure Content Understanding. (Preview)",
		Long: "Work with Azure Content Understanding using a native Go azd extension.\n\n" +
			"Manage analyzer definitions in your Microsoft Foundry resource.",
	})

	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.CompletionOptions = cobra.CompletionOptions{
		DisableDefaultCmd: true,
	}

	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})

	rootCmd.AddCommand(newVersionCommand(&extCtx.OutputFormat))
	rootCmd.AddCommand(newMetadataCommand(rootCmd))
	rootCmd.AddCommand(newAnalyzerCommand(extCtx, open))
	rootCmd.AddCommand(newAnalyzeCommand(extCtx, open))
	rootCmd.AddCommand(newDefaultsCommand(extCtx, open))
	rootCmd.AddCommand(newDoctorCommand(extCtx, open))
	rootCmd.AddCommand(newConfigCommand(extCtx))
	rootCmd.AddCommand(newEnvVarCommand(extCtx))
	rootCmd.AddCommand(newInfraCommand(extCtx, infraOpen))
	rootCmd.Example = `  azd ai cu analyze invoice.pdf
    Analyze one file using the configured analyzer, or prebuilt-layout if unset.

  azd ai cu analyzer list
    List the analyzers available in the connected resource.

  azd ai cu doctor
    Check authentication, resource access, and model configuration.`

	installHelp(rootCmd)
	protectCommandErrors(rootCmd)
	return rootCmd
}
