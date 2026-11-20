// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/cmd/add"
	"github.com/azure/azure-dev/cli/azd/internal/grpcserver"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/spf13/cobra"
)

const legacyAddActionName = "azd-add-legacy-action"

func getCmdAddHelpDescription(cmd *cobra.Command) string {
	return generateCmdHelpDescription(cmd.Short, []string{
		"Choose from items supplied by installed extensions.",
		"If none are available, use the built-in menu for compatible projects.",
	})
}

func newAddAction(
	locator ioc.ServiceLocator,
	console input.Console,
	options *internal.GlobalCommandOptions,
	envFlag internal.EnvFlag,
	activator *middleware.ExtensionActivator,
	registry *grpcserver.AddProviderRegistry,
) actions.Action {
	return add.NewDispatchAction(
		console,
		options.NoPrompt,
		func(ctx context.Context) ([]add.ProviderItem, func(), error) {
			cleanup, err := activator.StartAddProviders(ctx, envFlag.EnvironmentName, registry)
			if err != nil {
				return nil, cleanup, err
			}

			var detachOutput func()
			registered := registry.Items()
			items := make([]add.ProviderItem, 0, len(registered))
			for _, item := range registered {
				items = append(items, add.ProviderItem{
					ID: item.ID, DisplayName: item.DisplayName, Provider: item.Provider,
					Run: func(ctx context.Context) error {
						handles := console.Handles()
						detachOutput = item.AttachOutput(handles.Stdout, handles.Stderr)
						return item.Run(ctx)
					},
				})
			}
			return items, func() {
				if detachOutput != nil {
					defer detachOutput()
				}
				// Process IO can arrive after the RPC response. Keep forwarding until listener shutdown drains it.
				cleanup()
			}, nil
		},
		func() (actions.Action, error) {
			var legacy actions.Action
			err := locator.ResolveNamed(legacyAddActionName, &legacy)
			return legacy, err
		},
	)
}
