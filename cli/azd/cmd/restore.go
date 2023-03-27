// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
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
	//deprecate:flag hide --service
	_ = local.MarkHidden("service")
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
		Use:   "restore <service>",
		Short: "Restore application dependencies.",
	}
}

type restoreAction struct {
	flags          *restoreFlags
	args           []string
	console        input.Console
	azCli          azcli.AzCli
	azdCtx         *azdcontext.AzdContext
	env            *environment.Environment
	projectConfig  *project.ProjectConfig
	serviceManager project.ServiceManager
	commandRunner  exec.CommandRunner
}

func newRestoreAction(
	flags *restoreFlags,
	args []string,
	azCli azcli.AzCli,
	console input.Console,
	azdCtx *azdcontext.AzdContext,
	env *environment.Environment,
	projectConfig *project.ProjectConfig,
	serviceManager project.ServiceManager,
	commandRunner exec.CommandRunner,
) actions.Action {
	return &restoreAction{
		flags:          flags,
		args:           args,
		console:        console,
		azdCtx:         azdCtx,
		projectConfig:  projectConfig,
		serviceManager: serviceManager,
		azCli:          azCli,
		env:            env,
		commandRunner:  commandRunner,
	}
}

func (r *restoreAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if r.flags.serviceName != "" {
		fmt.Fprint(
			r.console.Handles().Stderr,
			//nolint:Lll
			output.WithWarningFormat("--service flag is no longer required. Simply run azd deploy <service> instead."))
	}

	targetServiceName := r.flags.serviceName
	if len(r.args) == 1 {
		targetServiceName = r.args[0]
	}

	if targetServiceName != "" && !r.projectConfig.HasService(targetServiceName) {
		return nil, fmt.Errorf("service name '%s' doesn't exist", targetServiceName)
	}

	count := 0

	// Collect all the tools we will need to do the restore and validate that
	// the are installed. When a single project is being deployed, we need just
	// the tools for that project, otherwise we need the tools from all project.
	allTools := []tools.ExternalTool{}
	for _, svc := range r.projectConfig.Services {
		if targetServiceName == "" || targetServiceName == svc.Name {
			requiredTools, err := r.serviceManager.GetRequiredTools(ctx, svc)
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
		if targetServiceName != "" && svc.Name != targetServiceName {
			continue
		}

		installMsg := fmt.Sprintf("Installing dependencies for %s service...", svc.Name)
		spinner := spin.NewSpinner(r.console.Handles().Stdout, installMsg)
		if err := spinner.Run(func() error {
			restoreTask := r.serviceManager.Restore(ctx, svc)
			go func() {
				for progress := range restoreTask.Progress() {
					log.Printf("Restore progress: %s\n", progress.Message)
				}
			}()
			_, err := restoreTask.Await()
			if err != nil {
				return err
			}
			return nil
		}); err != nil {
			return nil, err
		}

		count++
	}

	if targetServiceName != "" && count == 0 {
		return nil, fmt.Errorf("Dependencies were not restored (%s service was not found)", targetServiceName)
	}

	return nil, nil
}

func getCmdRestoreHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription(
		"Restore application dependencies.",
		[]string{
			formatHelpNote("Run this command to download and install all required dependencies so that you can build," +
				" run, and debug the application locally."),
			formatHelpNote(fmt.Sprintf("For the best local rn and debug experience, go to %s to learn how "+
				"to use the Visual Studio Code extension.",
				output.WithLinkFormat("https://aka.ms/azure-dev/vscode"),
			)),
		})
}

func getCmdRestoreHelpFooter(*cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		"Downloads and installs all application dependencies.": output.WithHighLightFormat("azd restore"),
		"Downloads and installs a specific application service " +
			"dependency, Individual services are listed in your azure.yaml file.": fmt.Sprintf("%s %s",
			output.WithHighLightFormat("azd restore <service>"),
			output.WithWarningFormat("[Service name]")),
	})
}
