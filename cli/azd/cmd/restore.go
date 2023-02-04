// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/spin"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type restoreFlags struct {
	global      *internal.GlobalCommandOptions
	serviceName string
	envFlag
}

func (r *restoreFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.StringVar(
		&r.serviceName,
		"service",
		"",
		//nolint:lll
		"Restores a specific service (when the string is unspecified, all services that are listed in the "+azdcontext.ProjectFileName+" file are restored).",
	)
}

func newRestoreFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *restoreFlags {
	flags := &restoreFlags{}
	flags.Bind(cmd.Flags(), global)
	flags.envFlag.Bind(cmd.Flags(), global)
	flags.global = global

	return flags
}

func restoreCmdDesign() *cobra.Command {
	return &cobra.Command{
		Use:   "restore",
		Short: "Restore app dependencies.",
		//nolint:lll
		Long: `Restore app dependencies.

Run this command to download and install all the required libraries so that you can build, run, and debug the app locally.

For the best local run and debug experience, go to https://aka.ms/azure-dev/vscode to learn how to use the Visual Studio Code extension.`,
	}
}

type restoreAction struct {
	flags         *restoreFlags
	console       input.Console
	azCli         azcli.AzCli
	azdCtx        *azdcontext.AzdContext
	env           *environment.Environment
	projectConfig *project.ProjectConfig
	commandRunner exec.CommandRunner
}

func newRestoreAction(
	flags *restoreFlags,
	azCli azcli.AzCli,
	console input.Console,
	azdCtx *azdcontext.AzdContext,
	env *environment.Environment,
	projectConfig *project.ProjectConfig,
	commandRunner exec.CommandRunner,
) actions.Action {
	return &restoreAction{
		flags:         flags,
		console:       console,
		azdCtx:        azdCtx,
		projectConfig: projectConfig,
		azCli:         azCli,
		env:           env,
		commandRunner: commandRunner,
	}
}

func (r *restoreAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if r.flags.serviceName != "" && !r.projectConfig.HasService(r.flags.serviceName) {
		return nil, fmt.Errorf("service name '%s' doesn't exist", r.flags.serviceName)
	}

	count := 0

	// Collect all the tools we will need to do the restore and validate that
	// the are installed. When a single project is being deployed, we need just
	// the tools for that project, otherwise we need the tools from all project.
	allTools := []tools.ExternalTool{}
	for _, svc := range r.projectConfig.Services {
		if r.flags.serviceName == "" || r.flags.serviceName == svc.Name {
			requiredTools, err := svc.GetRequiredTools(ctx, r.env, r.commandRunner)
			if err != nil {
				return nil, fmt.Errorf("failed getting required tools, %w", err)
			}

			allTools = append(allTools, requiredTools...)
		}
	}

	if err := tools.EnsureInstalled(ctx, tools.Unique(allTools)...); err != nil {
		return nil, err
	}

	for _, svc := range r.projectConfig.Services {
		if r.flags.serviceName != "" && svc.Name != r.flags.serviceName {
			continue
		}

		installMsg := fmt.Sprintf("Installing dependencies for %s service...", svc.Name)
		spinner := spin.NewSpinner(r.console.Handles().Stdout, installMsg)
		if err := spinner.Run(func() error {
			return svc.Restore(ctx, r.env, r.commandRunner)
		}); err != nil {
			return nil, err
		}

		count++
	}

	if r.flags.serviceName != "" && count == 0 {
		return nil, fmt.Errorf("Dependencies were not restored (%s service was not found)", r.flags.serviceName)
	}

	return nil, nil
}
