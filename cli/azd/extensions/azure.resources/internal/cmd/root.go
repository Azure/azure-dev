// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"

	"azure.resources/internal/provider"
	"azure.resources/internal/version"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// NewRootCommand creates the resources extension command tree.
func NewRootCommand() *cobra.Command {
	root, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name: "resources", Version: version.Version,
		Short: "Generate and provision Azure resources declared in azure.yaml.",
	})
	root.AddCommand(newAddCommand(extCtx))
	var resourcesProvider *provider.Provider
	listen := azdext.NewListenCommand(func(host *azdext.ExtensionHost) {
		resourcesProvider = configureResourcesProvider(host)
	})
	runListen := listen.RunE
	listen.RunE = func(cmd *cobra.Command, args []string) (err error) {
		// Stable provisioning has no remote Close; keep cleanup local to the listen session.
		defer func() {
			if resourcesProvider != nil {
				err = errors.Join(err, resourcesProvider.Close())
			}
		}()
		return runListen(cmd, args)
	}
	root.AddCommand(listen)
	root.AddCommand(azdext.NewMetadataCommand("1.0", "azure.resources", NewRootCommand))
	return root
}

func configureExtensionHost(host *azdext.ExtensionHost) {
	configureResourcesProvider(host)
}

func configureResourcesProvider(host *azdext.ExtensionHost) *provider.Provider {
	resourcesProvider := provider.New(host.Client())
	host.WithProvisioningProvider("resources", func() azdext.ProvisioningProvider {
		return resourcesProvider
	})
	return resourcesProvider
}
