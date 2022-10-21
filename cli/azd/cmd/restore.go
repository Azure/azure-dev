// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/spin"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type restoreFlags struct {
	global      *internal.GlobalCommandOptions
	serviceName string
}

func (r *restoreFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.StringVar(
		&r.serviceName,
		"service",
		"",
		//nolint:lll
		"Restores a specific service (when the string is unspecified, all services that are listed in the "+azdcontext.ProjectFileName+" file are restored).",
	)
	r.global = global
}

func restoreCmdDesign(global *internal.GlobalCommandOptions) (*cobra.Command, *restoreFlags) {
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore application dependencies.",
		//nolint:lll
		Long: `Restore application dependencies.

Run this command to download and install all the required libraries so that you can build, run, and debug the application locally.

For the best local run and debug experience, go to https://aka.ms/azure-dev/vscode to learn how to use the Visual Studio Code extension.`,
	}

	flags := &restoreFlags{}
	flags.Bind(cmd.Flags(), global)
	return cmd, flags
}

type restoreAction struct {
	flags   restoreFlags
	console input.Console
	azdCtx  *azdcontext.AzdContext
}

func newRestoreAction(flags restoreFlags, console input.Console, azdCtx *azdcontext.AzdContext) *restoreAction {
	return &restoreAction{
		flags:   flags,
		console: console,
		azdCtx:  azdCtx,
	}
}

func (i *restoreAction) PostRun(ctx context.Context, runResult error) error {
	return runResult
}

func (r *restoreAction) Run(ctx context.Context) error {
	if err := ensureProject(r.azdCtx.ProjectPath()); err != nil {
		return err
	}

	env, ctx, err := loadOrInitEnvironment(ctx, &r.flags.global.EnvironmentName, r.azdCtx, r.console)
	if err != nil {
		return fmt.Errorf("loading environment: %w", err)
	}

	proj, err := project.LoadProjectConfig(r.azdCtx.ProjectPath(), env)

	if err != nil {
		return fmt.Errorf("loading project: %w", err)
	}

	if r.flags.serviceName != "" && !proj.HasService(r.flags.serviceName) {
		return fmt.Errorf("service name '%s' doesn't exist", r.flags.serviceName)
	}

	count := 0

	// Collect all the tools we will need to do the restore and validate that
	// the are installed. When a single project is being deployed, we need just
	// the tools for that project, otherwise we need the tools from all project.
	allTools := []tools.ExternalTool{}
	for _, svc := range proj.Services {
		if r.flags.serviceName == "" || r.flags.serviceName == svc.Name {
			frameworkService, err := svc.GetFrameworkService(ctx, env)
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
		if r.flags.serviceName != "" && svc.Name != r.flags.serviceName {
			continue
		}

		installMsg := fmt.Sprintf("Installing dependencies for %s service...", svc.Name)
		frameworkService, err := svc.GetFrameworkService(ctx, env)
		if err != nil {
			return fmt.Errorf("getting framework services: %w", err)
		}

		spinner := spin.NewSpinner(r.console.Handles().Stdout, installMsg)
		if err = spinner.Run(func() error { return (*frameworkService).InstallDependencies(ctx) }); err != nil {
			return err
		}

		count++
	}

	if r.flags.serviceName != "" && count == 0 {
		return fmt.Errorf("Dependencies were not restored (%s service was not found)", r.flags.serviceName)
	}

	return nil
}
