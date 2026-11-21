// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/cmd/add"
	"github.com/azure/azure-dev/cli/azd/internal/grpcserver"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
)

func newAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add",
		Short: "Add a component to your project.",
	}
}

func getCmdAddHelpDescription(cmd *cobra.Command) string {
	return generateCmdHelpDescription(cmd.Short, []string{
		"Choose from items supplied by installed extensions.",
		"If no installed extension offers items, see available extensions that support azd add.",
	})
}

func newAddAction(
	console input.Console,
	options *internal.GlobalCommandOptions,
	envFlag internal.EnvFlag,
	activator *middleware.ExtensionActivator,
	registry *grpcserver.AddProviderRegistry,
	manager *extensions.Manager,
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
					Category:       item.Category,
					DisabledReason: item.DisabledReason,
					Run: func(ctx context.Context) error {
						if detachOutput != nil {
							detachOutput()
						}
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
		func(ctx context.Context) error {
			return suggestAddExtensions(ctx, manager)
		},
	)
}

func suggestAddExtensions(ctx context.Context, manager *extensions.Manager) error {
	matches, err := manager.FindExtensions(ctx, &extensions.FilterOptions{
		Capability: extensions.AddProviderCapability,
	})
	if err != nil {
		return fmt.Errorf("looking up extensions that support azd add: %w", err)
	}

	var entries []string
	for _, extension := range matches {
		version, err := manager.ResolveVersion(extension, "")
		if err != nil || !slices.Contains(version.Capabilities, extensions.AddProviderCapability) {
			continue
		}
		entries = append(entries, fmt.Sprintf("  - %s (source: %s)", extension.Id, extension.Source))
	}
	suggestion := "No extensions that support \"azd add\" were found in your configured sources."
	if len(entries) > 0 {
		suggestion = "Available extensions that support \"azd add\":\n" + strings.Join(entries, "\n") +
			"\n\nInstall one with " + output.WithHighLightFormat("azd extension install <id> --source <source>") +
			", then run " + output.WithHighLightFormat("azd add") + " again."
	}

	return &internal.ErrorWithSuggestion{
		Err:        errors.New("no installed extension offered anything to add"),
		Suggestion: suggestion,
	}
}
