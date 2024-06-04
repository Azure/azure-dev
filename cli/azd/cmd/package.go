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
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type packageFlags struct {
	all    bool
	global *internal.GlobalCommandOptions
	*internal.EnvFlag
	outputPath string
}

func newPackageFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *packageFlags {
	flags := &packageFlags{
		EnvFlag: &internal.EnvFlag{},
	}

	flags.Bind(cmd.Flags(), global)

	return flags
}

func (pf *packageFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	pf.EnvFlag.Bind(local, global)
	pf.global = global

	local.BoolVar(
		&pf.all,
		"all",
		false,
		"Packages all services that are listed in "+azdcontext.ProjectFileName,
	)
	local.StringVar(
		&pf.outputPath,
		"output-path",
		"",
		"File or folder path where the generated packages will be saved.",
	)
}

func newPackageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "package <service>",
		Short: fmt.Sprintf(
			"Packages the application's code to be deployed to Azure. %s",
			output.WithWarningFormat("(Beta)"),
		),
	}
	cmd.Args = cobra.MaximumNArgs(1)
	return cmd
}

type packageAction struct {
	flags          *packageFlags
	args           []string
	projectConfig  *project.ProjectConfig
	projectManager project.ProjectManager
	importManager  *project.ImportManager
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
	importManager *project.ImportManager,
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
		importManager:  importManager,
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

	startTime := time.Now()

	targetServiceName := ""
	if len(pa.args) == 1 {
		targetServiceName = pa.args[0]
	}

	targetServiceName, err := getTargetServiceName(
		ctx,
		pa.projectManager,
		pa.importManager,
		pa.projectConfig,
		string(project.ServiceEventPackage),
		targetServiceName,
		pa.flags.all,
	)
	if err != nil {
		return nil, err
	}

	if err := pa.projectManager.Initialize(ctx, pa.projectConfig); err != nil {
		return nil, err
	}

	if err := pa.projectManager.EnsureAllTools(ctx, pa.projectConfig, func(svc *project.ServiceConfig) bool {
		return targetServiceName == "" || svc.Name == targetServiceName
	}); err != nil {
		return nil, err
	}

	packageResults := map[string]*project.ServicePackageResult{}

	serviceTable, err := pa.importManager.ServiceStable(ctx, pa.projectConfig)
	if err != nil {
		return nil, err
	}
	serviceCount := len(serviceTable)
	for index, svc := range serviceTable {
		// TODO(ellismg): We need to figure out what packaging an containerized dotnet app means. For now, just skip it.
		//  We "package" the app during deploy when we call `dotnet publish /p:PublishProfile=DefaultContainer` to build
		//  and push the container image.
		//
		// Doing this skip here means that during `azd up` we don't show output like:
		// /* cSpell:disable */
		//
		// Packaging services (azd package)
		//
		// (✓) Done: Packaging service basketservice
		// - Package Output: /var/folders/6n/sxbj12js5ksg6ztn0kslqp400000gn/T/azd472091284
		//
		// (✓) Done: Packaging service catalogservice
		// - Package Output: /var/folders/6n/sxbj12js5ksg6ztn0kslqp400000gn/T/azd2265185954
		//
		// (✓) Done: Packaging service frontend
		// - Package Output: /var/folders/6n/sxbj12js5ksg6ztn0kslqp400000gn/T/azd2956031596
		//
		// /* cSpell:enable */
		// Which is nice - since the above is not the package that we publish (instead it's the raw output of
		// `dotnet publish`, as if you were going to run on App Service.).
		//
		// With .NET 8, we'll be able to build just the container image, by setting ContainerArchiveOutputPath
		// as a property when we run `dotnet publish`.  If we set this to the filepath of a tgz (doesn't need to exist)
		// the the action will just produce a container image and save it to that tgz, as `docker save` would have. It will
		// not push the container image.
		//
		// It's probably right for us to think about "package" for a containerized application as meaning "produce the tgz"
		// of the image, as would be done by `docker save` and then do this for both DotNetContainerAppTargets and
		// ContainerAppTargets.
		if svc.Host == project.DotNetContainerAppTarget {
			continue
		}

		stepMessage := fmt.Sprintf("Packaging service %s", svc.Name)
		pa.console.ShowSpinner(ctx, stepMessage, input.Step)

		// Skip this service if both cases are true:
		// 1. The user specified a service name
		// 2. This service is not the one the user specified
		if targetServiceName != "" && targetServiceName != svc.Name {
			pa.console.StopSpinner(ctx, stepMessage, input.StepSkipped)
			continue
		}

		options := &project.PackageOptions{OutputPath: pa.flags.outputPath}
		packageTask := pa.serviceManager.Package(ctx, svc, nil, options)
		done := make(chan struct{})
		go func() {
			for packageProgress := range packageTask.Progress() {
				progressMessage := fmt.Sprintf("Packaging service %s (%s)", svc.Name, packageProgress.Message)
				pa.console.ShowSpinner(ctx, progressMessage, input.Step)
			}
			close(done)
		}()

		packageResult, err := packageTask.Await()
		// adding a few seconds to wait for all async ops to be flush
		<-done
		pa.console.StopSpinner(ctx, stepMessage, input.GetStepResultFormat(err))

		if err != nil {
			return nil, err
		}
		packageResults[svc.Name] = packageResult

		// report package output
		pa.console.MessageUxItem(ctx, packageResult)
		if index < serviceCount-1 {
			pa.console.Message(ctx, "")
		}
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
			Header: fmt.Sprintf("Your application was packaged for Azure in %s.", ux.DurationAsText(since(startTime))),
		},
	}, nil
}

func getCmdPackageHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription(fmt.Sprintf(
		"Packages application's code to be deployed to Azure. %s",
		output.WithWarningFormat("(Beta)"),
	), []string{
		formatHelpNote(
			"By default, packages all services listed in 'azure.yaml' in the current directory," +
				" or the service described in the project that matches the current directory."),
		formatHelpNote(
			fmt.Sprintf("When %s is set, only the specific service is packaged.", output.WithHighLightFormat("<service>"))),
		formatHelpNote("After the packaging is complete, the package locations are printed."),
	})
}

func getCmdPackageHelpFooter(*cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		"Packages all services in the current project to Azure.": output.WithHighLightFormat("azd package --all"),
		"Packages the service named 'api' to Azure.":             output.WithHighLightFormat("azd package api"),
		"Packages the service named 'web' to Azure.":             output.WithHighLightFormat("azd package web"),
		"Packages all services to the specified output path.": output.WithHighLightFormat(
			"azd package --output-path ./dist",
		),
		"Packages the service named 'api' to the specified output path.": output.WithHighLightFormat(
			"azd package api --output-path ./dist/api.zip",
		),
	})
}
