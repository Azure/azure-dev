// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type restoreFlags struct {
	all         bool
	global      *internal.GlobalCommandOptions
	serviceName string
	envFlag
}

func (r *restoreFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.BoolVar(
		&r.all,
		"all",
		false,
		"Restores all services that are listed in "+azdcontext.ProjectFileName,
	)
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

func newRestoreCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restore <service>",
		Short: "Restores the application's dependencies.",
	}
	cmd.Args = cobra.MaximumNArgs(1)
	return cmd
}

type restoreAction struct {
	flags          *restoreFlags
	args           []string
	console        input.Console
	formatter      output.Formatter
	writer         io.Writer
	azdCtx         *azdcontext.AzdContext
	env            *environment.Environment
	projectConfig  *project.ProjectConfig
	projectManager project.ProjectManager
	serviceManager project.ServiceManager
	commandRunner  exec.CommandRunner
}

func newRestoreAction(
	flags *restoreFlags,
	args []string,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
	azdCtx *azdcontext.AzdContext,
	env *environment.Environment,
	projectConfig *project.ProjectConfig,
	projectManager project.ProjectManager,
	serviceManager project.ServiceManager,
	commandRunner exec.CommandRunner,
) actions.Action {
	return &restoreAction{
		flags:          flags,
		args:           args,
		console:        console,
		formatter:      formatter,
		writer:         writer,
		azdCtx:         azdCtx,
		projectConfig:  projectConfig,
		projectManager: projectManager,
		serviceManager: serviceManager,
		env:            env,
		commandRunner:  commandRunner,
	}
}

type RestoreResult struct {
	Timestamp time.Time                                `json:"timestamp"`
	Services  map[string]*project.ServiceRestoreResult `json:"services"`
}

func (ra *restoreAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	// Command title
	ra.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: "Restoring services (azd restore)",
	})

	serviceNameWarningCheck(ra.console, ra.flags.serviceName, "restore")

	targetServiceName := ra.flags.serviceName
	if len(ra.args) == 1 {
		targetServiceName = ra.args[0]
	}

	targetServiceName, err := getTargetServiceName(
		ctx,
		ra.projectManager,
		ra.projectConfig,
		string(project.ServiceEventRestore),
		targetServiceName,
		ra.flags.all,
	)
	if err != nil {
		return nil, err
	}

	if err := ra.projectManager.Initialize(ctx, ra.projectConfig); err != nil {
		return nil, err
	}

	if err := ra.projectManager.EnsureFrameworkTools(ctx, ra.projectConfig, func(svc *project.ServiceConfig) bool {
		return targetServiceName == "" || svc.Name == targetServiceName
	}); err != nil {
		return nil, err
	}

	restoreResults := map[string]*project.ServiceRestoreResult{}

	for _, svc := range ra.projectConfig.GetServicesStable() {
		stepMessage := fmt.Sprintf("Restoring service %s", svc.Name)
		ra.console.ShowSpinner(ctx, stepMessage, input.Step)

		// Skip this service if both cases are true:
		// 1. The user specified a service name
		// 2. This service is not the one the user specified
		if targetServiceName != "" && targetServiceName != svc.Name {
			ra.console.StopSpinner(ctx, stepMessage, input.StepSkipped)
			continue
		}

		restoreTask := ra.serviceManager.Restore(ctx, svc)
		go func() {
			for restoreProgress := range restoreTask.Progress() {
				progressMessage := fmt.Sprintf("Restoring service %s (%s)", svc.Name, restoreProgress.Message)
				ra.console.ShowSpinner(ctx, progressMessage, input.Step)
			}
		}()

		restoreResult, err := restoreTask.Await()
		if err != nil {
			ra.console.StopSpinner(ctx, stepMessage, input.StepFailed)
			return nil, err
		}

		ra.console.StopSpinner(ctx, stepMessage, input.StepDone)
		restoreResults[svc.Name] = restoreResult
	}

	if ra.formatter.Kind() == output.JsonFormat {
		restoreResult := RestoreResult{
			Timestamp: time.Now(),
			Services:  restoreResults,
		}

		if fmtErr := ra.formatter.Format(restoreResult, ra.writer, nil); fmtErr != nil {
			return nil, fmt.Errorf("restore result could not be displayed: %w", fmtErr)
		}
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: "Your Azure app has been restored!",
		},
	}, nil
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
