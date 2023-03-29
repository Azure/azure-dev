package cmd

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type packageFlags struct {
	serviceName string
	global      *internal.GlobalCommandOptions
	*envFlag
}

func newPackageFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *packageFlags {
	flags := &packageFlags{
		global:  global,
		envFlag: newEnvFlag(cmd, global),
	}

	flags.Bind(cmd.Flags(), global)

	return flags
}

func (pf *packageFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.StringVar(
		&pf.serviceName,
		"service",
		"",
		//nolint:lll
		"Deploys a specific service (when the string is unspecified, all services that are listed in the "+azdcontext.ProjectFileName+" file are deployed).",
	)
	//deprecate:flag hide --service
	_ = local.MarkHidden("service")
}

func newPackageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "package <service>",
		Short:  "Packages the application's code to be deployed to Azure.",
		Hidden: true,
	}
	cmd.Args = cobra.MaximumNArgs(1)
	return cmd
}

type packageAction struct {
	flags          *packageFlags
	args           []string
	projectConfig  *project.ProjectConfig
	projectManager project.ProjectManager
	serviceManager project.ServiceManager
	console        input.Console
	formatter      output.Formatter
	writer         io.Writer
}

func newPackageAction(
	flags *packageFlags,
	args []string,
	projectConfig *project.ProjectConfig,
	projectManager project.ProjectManager,
	serviceManager project.ServiceManager,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
) actions.Action {
	return &packageAction{
		flags:          flags,
		args:           args,
		projectConfig:  projectConfig,
		projectManager: projectManager,
		serviceManager: serviceManager,
		console:        console,
		formatter:      formatter,
		writer:         writer,
	}
}

type PackageResult struct {
	Timestamp time.Time                                `json:"timestamp"`
	Services  map[string]*project.ServicePackageResult `json:"services"`
}

func (pa *packageAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	// Command title
	pa.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: "Packaging services (azd package)",
	})

	targetServiceName := pa.flags.serviceName
	if len(pa.args) == 1 {
		targetServiceName = pa.args[0]
	}

	serviceNameWarningCheck(pa.console, pa.flags.serviceName, "package")

	if targetServiceName != "" && !pa.projectConfig.HasService(targetServiceName) {
		return nil, fmt.Errorf("service name '%s' doesn't exist", targetServiceName)
	}

	if err := pa.projectManager.Initialize(ctx, pa.projectConfig); err != nil {
		return nil, err
	}

	if err := pa.ensureTools(ctx, targetServiceName); err != nil {
		return nil, err
	}

	packageResults := map[string]*project.ServicePackageResult{}

	for _, svc := range pa.projectConfig.Services {
		stepMessage := fmt.Sprintf("Packaging service %s", svc.Name)
		pa.console.ShowSpinner(ctx, stepMessage, input.Step)

		// Skip this service if both cases are true:
		// 1. The user specified a service name
		// 2. This service is not the one the user specified
		if targetServiceName != "" && targetServiceName != svc.Name {
			pa.console.StopSpinner(ctx, stepMessage, input.StepSkipped)
			continue
		}

		packageTask := pa.serviceManager.Package(ctx, svc, nil)
		go func() {
			for packageProgress := range packageTask.Progress() {
				progressMessage := fmt.Sprintf("Packaging service %s (%s)", svc.Name, packageProgress.Message)
				pa.console.ShowSpinner(ctx, progressMessage, input.Step)
			}
		}()

		packageResult, err := packageTask.Await()
		if err != nil {
			pa.console.StopSpinner(ctx, stepMessage, input.StepFailed)
			return nil, err
		}

		pa.console.StopSpinner(ctx, stepMessage, input.StepDone)
		packageResults[svc.Name] = packageResult

		// report package output
		pa.console.MessageUxItem(ctx, packageResult)
	}

	if pa.formatter.Kind() == output.JsonFormat {
		packageResult := PackageResult{
			Timestamp: time.Now(),
			Services:  packageResults,
		}

		if fmtErr := pa.formatter.Format(packageResult, pa.writer, nil); fmtErr != nil {
			return nil, fmt.Errorf("package result could not be displayed: %w", fmtErr)
		}
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: "Your Azure app has been packaged!",
		},
	}, nil
}

func (b *packageAction) ensureTools(ctx context.Context, targetServiceName string) error {
	// Collect all the tools we will need to do the deployment and validate that
	// the are installed. When a single project is being deployed, we need just
	// the tools for that project, otherwise we need the tools from all project.
	var allTools []tools.ExternalTool
	for _, svc := range b.projectConfig.Services {
		if targetServiceName == "" || targetServiceName == svc.Name {
			serviceTools, err := b.serviceManager.GetRequiredTools(ctx, svc)
			if err != nil {
				return err
			}

			allTools = append(allTools, serviceTools...)
		}
	}

	if err := tools.EnsureInstalled(ctx, tools.Unique(allTools)...); err != nil {
		return fmt.Errorf("failed getting required tools for project")
	}

	return nil
}
