package cmd

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type buildFlags struct {
	*envFlag
	all    bool
	global *internal.GlobalCommandOptions
	only   bool
}

func newBuildFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *buildFlags {
	flags := &buildFlags{
		envFlag: &envFlag{},
	}

	flags.Bind(cmd.Flags(), global)

	return flags
}

func (bf *buildFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	bf.envFlag.Bind(local, global)
	bf.global = global

	local.BoolVar(
		&bf.all,
		"all",
		false,
		"Deploys all services that are listed in "+azdcontext.ProjectFileName,
	)
}

func newBuildCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "build <service>",
		Short:  "Builds the application's code.",
		Hidden: true,
	}
	cmd.Args = cobra.MaximumNArgs(1)
	return cmd
}

type buildAction struct {
	flags                    *buildFlags
	args                     []string
	projectConfig            *project.ProjectConfig
	projectManager           project.ProjectManager
	serviceManager           project.ServiceManager
	console                  input.Console
	formatter                output.Formatter
	writer                   io.Writer
	middlewareRunner         middleware.MiddlewareContext
	restoreActionInitializer actions.ActionInitializer[*restoreAction]
}

func newBuildAction(
	flags *buildFlags,
	args []string,
	projectConfig *project.ProjectConfig,
	projectManager project.ProjectManager,
	serviceManager project.ServiceManager,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
	middlewareRunner middleware.MiddlewareContext,
	restoreActionInitializer actions.ActionInitializer[*restoreAction],

) actions.Action {
	return &buildAction{
		flags:                    flags,
		args:                     args,
		projectConfig:            projectConfig,
		projectManager:           projectManager,
		serviceManager:           serviceManager,
		console:                  console,
		formatter:                formatter,
		writer:                   writer,
		middlewareRunner:         middlewareRunner,
		restoreActionInitializer: restoreActionInitializer,
	}
}

type BuildResult struct {
	Timestamp time.Time                              `json:"timestamp"`
	Services  map[string]*project.ServiceBuildResult `json:"services"`
}

func (ba *buildAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if !ba.flags.only {
		restoreAction, err := ba.restoreActionInitializer()
		restoreAction.flags.all = ba.flags.all
		restoreAction.args = ba.args
		if err != nil {
			return nil, err
		}

		buildOptions := &middleware.Options{CommandPath: "restore"}
		_, err = ba.middlewareRunner.RunChildAction(ctx, buildOptions, restoreAction)
		if err != nil {
			return nil, err
		}
	}

	// Command title
	ba.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: "Building services (azd build)",
	})

	targetServiceName := ""
	if len(ba.args) == 1 {
		targetServiceName = ba.args[0]
	}

	targetServiceName, err := getTargetServiceName(
		ctx,
		ba.projectManager,
		ba.projectConfig,
		string(project.ServiceEventBuild),
		targetServiceName,
		ba.flags.all,
	)
	if err != nil {
		return nil, err
	}

	if err := ba.projectManager.EnsureFrameworkTools(ctx, ba.projectConfig, func(svc *project.ServiceConfig) bool {
		return targetServiceName == "" || svc.Name == targetServiceName
	}); err != nil {
		return nil, err
	}

	if err := ba.projectManager.Initialize(ctx, ba.projectConfig); err != nil {
		return nil, err
	}

	buildResults := map[string]*project.ServiceBuildResult{}

	for _, svc := range ba.projectConfig.GetServicesStable() {
		stepMessage := fmt.Sprintf("Building service %s", svc.Name)
		ba.console.ShowSpinner(ctx, stepMessage, input.Step)

		// Skip this service if both cases are true:
		// 1. The user specified a service name
		// 2. This service is not the one the user specified
		if targetServiceName != "" && targetServiceName != svc.Name {
			ba.console.StopSpinner(ctx, stepMessage, input.StepSkipped)
			continue
		}

		buildTask := ba.serviceManager.Build(ctx, svc, nil)
		go func() {
			for buildProgress := range buildTask.Progress() {
				progressMessage := fmt.Sprintf("Building service %s (%s)", svc.Name, buildProgress.Message)
				ba.console.ShowSpinner(ctx, progressMessage, input.Step)
			}
		}()

		buildResult, err := buildTask.Await()
		if err != nil {
			ba.console.StopSpinner(ctx, stepMessage, input.StepFailed)
			return nil, err
		}

		ba.console.StopSpinner(ctx, stepMessage, input.StepDone)
		buildResults[svc.Name] = buildResult

		// report build outputs
		ba.console.MessageUxItem(ctx, buildResult)
	}

	if ba.formatter.Kind() == output.JsonFormat {
		buildResult := BuildResult{
			Timestamp: time.Now(),
			Services:  buildResults,
		}

		if fmtErr := ba.formatter.Format(buildResult, ba.writer, nil); fmtErr != nil {
			return nil, fmt.Errorf("build result could not be displayed: %w", fmtErr)
		}
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: "Your Azure app has been built!",
		},
	}, nil
}
