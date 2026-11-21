// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"

	"azure.resources/internal/provider"
	"azure.resources/internal/version"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// NewRootCommand creates the resources extension command tree.
func NewRootCommand() *cobra.Command {
	root, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name: "resources", Version: version.Version,
		Short: "Generate and provision Azure resources declared in azure.yaml.",
	})
	root.AddCommand(newAddCommand(extCtx))
	var resourcesProvider *provider.Provider
	var addSucceeded atomic.Bool
	var listen *cobra.Command
	listen = azdext.NewListenCommand(func(host *azdext.ExtensionHost) {
		resourcesProvider = configureResourcesProvider(host)
		ctx := azdext.WithAccessToken(listen.Context())
		client := host.Client()
		runAdd := newAddProviderHandler(client, extCtx, listen.OutOrStdout())
		host.WithBetaAddProvider(projectAddProviderItems(ctx, client.Project()), func(ctx context.Context, id string) error {
			addSucceeded.Store(false)
			err := runAdd(ctx, id)
			addSucceeded.Store(err == nil && ctx.Err() == nil)
			return err
		})
	})
	runListen := listen.RunE
	listen.RunE = func(cmd *cobra.Command, args []string) (err error) {
		addSucceeded.Store(false)
		// Stable provisioning has no remote Close; keep cleanup local to the listen session.
		defer func() {
			if resourcesProvider != nil {
				err = errors.Join(err, resourcesProvider.Close())
			}
		}()
		err = runListen(cmd, args)
		return addListenerError(addSucceeded.Load(), err)
	}
	root.AddCommand(listen)
	root.AddCommand(azdext.NewMetadataCommand("1.0", "azure.resources", NewRootCommand))
	return root
}

func addListenerError(addSucceeded bool, err error) error {
	// Core can reset the listener connection while tearing down a successful add.
	if addSucceeded && status.Code(err) == codes.Unavailable &&
		strings.HasPrefix(err.Error(), "receiver error: stream receive failed: ") &&
		strings.HasSuffix(err.Error(), "connection reset by peer") {
		return nil
	}
	return err
}

func configureExtensionHost(host *azdext.ExtensionHost) {
	configureResourcesProvider(host)
}

func configureResourcesProvider(host *azdext.ExtensionHost) *provider.Provider {
	resourcesProvider := provider.New(host.Client())
	host.WithProvisioningProvider("resources", func() azdext.ProvisioningProvider {
		return resourcesProvider
	})
	host.WithBetaInfraGenerationProvider("resources", provider.NewGenerationProvider())
	return resourcesProvider
}
