// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/spin"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func restoreCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	cmd := commands.Build(
		&restoreAction{
			rootOptions: rootOptions,
		},
		rootOptions,
		"restore",
		"Restore application dependencies",
		`Restore application dependencies

Run this command to install/download all the required libraries so that you can build, run, and debug the application locally.

For best local run and debug experience, refer to https://aka.ms/azure-dev/vscode to leverage the VS Code extension.`,
	)
	cmd.Flags().BoolP("help", "h", false, "Help for "+cmd.Name())
	return cmd
}

type restoreAction struct {
	rootOptions *commands.GlobalCommandOptions
	serviceName string
}

func (r *restoreAction) SetupFlags(persis, local *pflag.FlagSet) {
	local.StringVar(&r.serviceName, "service", "", "Restores dependencies for a specific service (when unset, dependencies for all services listed in "+environment.ProjectFileName+" are restored)")
}

func (r *restoreAction) Run(ctx context.Context, _ *cobra.Command, args []string, azdCtx *environment.AzdContext) error {
	proj, err := project.LoadProjectConfig(azdCtx.ProjectPath(), &environment.Environment{})
	if err := ensureProject(azdCtx.ProjectPath()); err != nil {
		return err
	}

	if err != nil {
		return fmt.Errorf("loading project: %w", err)
	}

	if r.serviceName != "" && !proj.HasService(r.serviceName) {
		return fmt.Errorf("service name '%s' doesn't exist", r.serviceName)
	}

	count := 0

	// Collect all the tools we will need to do the restore and validate that
	// the are installed. When a single project is being deployed, we need just
	// the tools for that project, otherwise we need the tools from all project.
	allTools := []tools.ExternalTool{}
	for _, svc := range proj.Services {
		if r.serviceName == "" || r.serviceName == svc.Name {
			frameworkService, err := svc.GetFrameworkService(ctx, &environment.Environment{})
			if err != nil {
				return fmt.Errorf("getting framework services: %w", err)
			}
			requiredTools := (*frameworkService).RequiredExternalTools()
			allTools = append(allTools, requiredTools...)
		}
	}

	if err := tools.EnsureInstalled(ctx, tools.Unique(allTools)...); err != nil {
		return err
	}

	for _, svc := range proj.Services {
		if r.serviceName != "" && svc.Name != r.serviceName {
			continue
		}

		installMsg := fmt.Sprintf("Installing dependencies for %s service...", svc.Name)
		frameworkService, err := svc.GetFrameworkService(ctx, &environment.Environment{})
		if err != nil {
			return fmt.Errorf("getting framework services: %w", err)
		}

		if err = spin.Run(installMsg, func() error { return (*frameworkService).InstallDependencies(ctx) }); err != nil {
			return err
		}

		count++
	}

	if r.serviceName != "" && count == 0 {
		return fmt.Errorf("Dependencies were not restored (%s service was not found)", r.serviceName)
	}

	return nil
}
