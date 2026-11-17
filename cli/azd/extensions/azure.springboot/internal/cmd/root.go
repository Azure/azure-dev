// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"azurespringboot/internal/version"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

const extensionID = "azure.springboot"

// NewRootCommand creates the Spring Boot extension command.
func NewRootCommand() *cobra.Command {
	root, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name:    extensionID,
		Short:   "Prepare Spring Boot services for Azure Container Apps.",
		Version: version.Version,
	})
	root.SilenceUsage = true
	root.SilenceErrors = true
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetHelpCommand(&cobra.Command{Hidden: true})

	root.AddCommand(azdext.NewListenCommand(func(host *azdext.ExtensionHost) {
		configureExtensionHostWithNoPrompt(host, extCtx.NoPrompt)
	}))
	root.AddCommand(azdext.NewMetadataCommand("1.0", extensionID, NewRootCommand))
	return root
}
