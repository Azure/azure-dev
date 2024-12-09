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
	"github.com/azure/azure-dev/cli/azd/pkg/async"
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
	internal.EnvFlag
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
	flags.EnvFlag.Bind(cmd.Flags(), global)
	flags.global = global

	return flags
}

func newRestoreCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restore <service>",
		Short: fmt.Sprintf("Restores the application's dependencies. %s", output.WithWarningFormat("(Beta)")),
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
	importManager  *project.ImportManager
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
	importManager *project.ImportManager,
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
		importManager:  importManager,
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

	startTime := time.Now()

	serviceNameWarningCheck(ra.console, ra.flags.serviceName, "restore")

	targetServiceName := ra.flags.serviceName
	if len(ra.args) == 1 {
		targetServiceName = ra.args[0]
	}

	targetServiceName, err := getTargetServiceName(
		ctx,
		ra.projectManager,
		ra.importManager,
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

	if err := ra.projectManager.EnsureRestoreTools(ctx, ra.projectConfig, func(svc *project.ServiceConfig) bool {
		return targetServiceName == "" || svc.Name == targetServiceName
	}); err != nil {
		return nil, err
	}

	restoreResults := map[string]*project.ServiceRestoreResult{}
	stableServices, err := ra.importManager.ServiceStable(ctx, ra.projectConfig)
	if err != nil {
		return nil, err
	}

	for _, svc := range stableServices {
		stepMessage := fmt.Sprintf("Restoring service %s", svc.Name)
		ra.console.ShowSpinner(ctx, stepMessage, input.Step)

		// Skip this service if both cases are true:
		// 1. The user specified a service name
		// 2. This service is not the one the user specified
		if targetServiceName != "" && targetServiceName != svc.Name {
			ra.console.StopSpinner(ctx, stepMessage, input.StepSkipped)
			continue
		}

		restoreResult, err := async.RunWithProgress(
			func(buildProgress project.ServiceProgress) {
				progressMessage := fmt.Sprintf("Building service %s (%s)", svc.Name, buildProgress.Message)
				ra.console.ShowSpinner(ctx, progressMessage, input.Step)
			},
			func(progress *async.Progress[project.ServiceProgress]) (*project.ServiceRestoreResult, error) {
				return ra.serviceManager.Restore(ctx, svc, progress)
			},
		)

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
			Header: fmt.Sprintf(
				"Your applications dependencies were restored in %s.", ux.DurationAsText(since(startTime))),
		},
	}, nil
}

func getCmdRestoreHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription(
		fmt.Sprintf("Restore application dependencies. %s", output.WithWarningFormat("(Beta)")),
		[]string{
			formatHelpNote("Run this command to download and install all required dependencies so that you can build," +
				" run, and debug the application locally."),
			formatHelpNote(fmt.Sprintf("For the best local run and debug experience, go to %s to learn how "+
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
