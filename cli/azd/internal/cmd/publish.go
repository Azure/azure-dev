// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type PublishFlags struct {
	ServiceName string
	All         bool
	Image       string
	Tag         string
	FromPackage string
	global      *internal.GlobalCommandOptions
	*internal.EnvFlag
}

func (f *PublishFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.EnvFlag = &internal.EnvFlag{}
	f.EnvFlag.Bind(local, global)
	f.global = global

	local.BoolVar(
		&f.All,
		"all",
		false,
		"Deploys all services that are listed in "+azdcontext.ProjectFileName,
	)

	local.StringVar(
		&f.Image,
		"image",
		"",
		"Specifies a custom image name for the container published to the registry.",
	)

	local.StringVar(
		&f.Tag,
		"tag",
		"",
		"Specifies a custom tag for the container image published to the registry.",
	)

	local.StringVar(
		&f.FromPackage,
		"from-package",
		"",
		"Publishes the service from a container image (image tag).",
	)
}

func NewPublishFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *PublishFlags {
	flags := &PublishFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func NewPublishCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "publish <service>",
		Short: "Publish a service to a container registry.",
	}
	cmd.Args = cobra.MaximumNArgs(1)
	return cmd
}

func NewPublishAction(
	flags *PublishFlags,
	args []string,
	projectConfig *project.ProjectConfig,
	projectManager project.ProjectManager,
	serviceManager project.ServiceManager,
	resourceManager project.ResourceManager,
	azdCtx *azdcontext.AzdContext,
	environment *environment.Environment,
	accountManager account.Manager,
	cloud *cloud.Cloud,
	azCli *azapi.AzureClient,
	commandRunner exec.CommandRunner,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
	alphaFeatureManager *alpha.FeatureManager,
	importManager *project.ImportManager,
) actions.Action {
	return &PublishAction{
		flags:               flags,
		args:                args,
		projectConfig:       projectConfig,
		azdCtx:              azdCtx,
		env:                 environment,
		projectManager:      projectManager,
		serviceManager:      serviceManager,
		resourceManager:     resourceManager,
		accountManager:      accountManager,
		azCli:               azCli,
		formatter:           formatter,
		writer:              writer,
		console:             console,
		commandRunner:       commandRunner,
		alphaFeatureManager: alphaFeatureManager,
		importManager:       importManager,
	}
}

type PublishAction struct {
	flags               *PublishFlags
	args                []string
	projectConfig       *project.ProjectConfig
	azdCtx              *azdcontext.AzdContext
	env                 *environment.Environment
	projectManager      project.ProjectManager
	serviceManager      project.ServiceManager
	resourceManager     project.ResourceManager
	accountManager      account.Manager
	azCli               *azapi.AzureClient
	formatter           output.Formatter
	writer              io.Writer
	console             input.Console
	commandRunner       exec.CommandRunner
	alphaFeatureManager *alpha.FeatureManager
	importManager       *project.ImportManager
}

type PublishResult struct {
	Timestamp time.Time                                `json:"timestamp"`
	Services  map[string]*project.ServicePublishResult `json:"services"`
}

func (pa *PublishAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	targetServiceName := pa.flags.ServiceName
	if len(pa.args) == 1 {
		targetServiceName = pa.args[0]
	}

	if pa.env.GetSubscriptionId() == "" {
		return nil, errors.New(
			"infrastructure has not been provisioned. Run `azd provision`",
		)
	}

	targetServiceName, err := getTargetServiceName(
		ctx,
		pa.projectManager,
		pa.importManager,
		pa.projectConfig,
		string(project.ServiceEventPublish),
		targetServiceName,
		pa.flags.All,
	)
	if err != nil {
		return nil, err
	}

	// Validate that --tag or --image requires a specific service
	if pa.flags.All && (pa.flags.Image != "" || pa.flags.Tag != "") {
		return nil, errors.New(
			//nolint:lll
			"'--tag' and '--image' cannot be specified when '--all' is set. Specify a specific service by passing a <service>")
	}

	if targetServiceName == "" && (pa.flags.Image != "" || pa.flags.Tag != "") {
		return nil, errors.New(
			//nolint:lll
			"'--tag' and '--image' cannot be specified when publishing all services. Specify a specific service by passing a <service>",
		)
	}

	if pa.flags.All && pa.flags.FromPackage != "" {
		return nil, errors.New(
			"'--from-package' cannot be specified when '--all' is set. Specify a specific service by passing a <service>")
	}

	if targetServiceName == "" && pa.flags.FromPackage != "" {
		return nil, errors.New(
			//nolint:lll
			"'--from-package' cannot be specified when publishing all services. Specify a specific service by passing a <service>",
		)
	}

	// Create publish options from flags
	publishOptions := &project.PublishOptions{
		Overwrite: true, // publish always overwrites
	}

	if err := pa.projectManager.Initialize(ctx, pa.projectConfig); err != nil {
		return nil, err
	}

	if err := pa.projectManager.EnsureServiceTargetTools(ctx, pa.projectConfig, func(svc *project.ServiceConfig) bool {
		return targetServiceName == "" || svc.Name == targetServiceName
	}); err != nil {
		return nil, err
	}

	// Apply image and tag overrides to service configurations before processing
	for _, svc := range pa.projectConfig.Services {
		if targetServiceName != "" && svc.Name != targetServiceName {
			continue
		}

		// Override service config with command-line flags
		if pa.flags.Image != "" {
			svc.Docker.Image = osutil.NewExpandableString(pa.flags.Image)
		}

		if pa.flags.Tag != "" {
			svc.Docker.Tag = osutil.NewExpandableString(pa.flags.Tag)
		}
	}

	// Command title
	pa.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: "Publishing services (azd publish)",
	})

	startTime := time.Now()

	publishResults := map[string]*project.ServicePublishResult{}
	stableServices, err := pa.importManager.ServiceStable(ctx, pa.projectConfig)
	if err != nil {
		return nil, err
	}

	for _, svc := range stableServices {
		stepMessage := fmt.Sprintf("Publishing service %s", svc.Name)
		pa.console.ShowSpinner(ctx, stepMessage, input.Step)

		// Skip this service if both cases are true:
		// 1. The user specified a service name
		// 2. This service is not the one the user specified
		if targetServiceName != "" && targetServiceName != svc.Name {
			pa.console.StopSpinner(ctx, stepMessage, input.StepSkipped)
			continue
		}

		if alphaFeatureId, isAlphaFeature := alpha.IsFeatureKey(string(svc.Host)); isAlphaFeature {
			// alpha feature on/off detection for host is done during initialization.
			// This is just for displaying the warning during publishing.
			pa.console.WarnForFeature(ctx, alphaFeatureId)
		}

		// Check if this service is a container app
		if svc.Host != project.ContainerAppTarget {
			pa.console.StopSpinner(ctx, stepMessage, input.StepSkipped)
			pa.console.MessageUxItem(ctx, &ux.WarningMessage{
				Description: fmt.Sprintf(
					"'publish' is only supported for container app services, but service '%s' has host type '%s'",
					svc.Name, svc.Host),
			})
			continue
		}

		var packageResult *project.ServicePackageResult

		if pa.flags.FromPackage != "" {
			// --from-package set, skip packaging
			packageResult = &project.ServicePackageResult{
				PackagePath: pa.flags.FromPackage,
			}
		} else {
			//  --from-package not set, automatically package the application
			packageResult, err = async.RunWithProgress(
				func(packageProgress project.ServiceProgress) {
					progressMessage := fmt.Sprintf("Packaging service %s (%s)", svc.Name, packageProgress.Message)
					pa.console.ShowSpinner(ctx, progressMessage, input.Step)
				},
				func(progress *async.Progress[project.ServiceProgress]) (*project.ServicePackageResult, error) {
					return pa.serviceManager.Package(ctx, svc, nil, progress, nil)
				},
			)

			if err != nil {
				pa.console.StopSpinner(ctx, stepMessage, input.StepFailed)
				return nil, err
			}
		}

		publishResult, err := async.RunWithProgress(
			func(publishProgress project.ServiceProgress) {
				progressMessage := fmt.Sprintf("Publishing service %s (%s)", svc.Name, publishProgress.Message)
				pa.console.ShowSpinner(ctx, progressMessage, input.Step)
			},
			func(progress *async.Progress[project.ServiceProgress]) (*project.ServicePublishResult, error) {
				return pa.serviceManager.Publish(ctx, svc, packageResult, progress, publishOptions)
			},
		)

		// clean up for packages automatically created in temp dir
		if pa.flags.FromPackage == "" && strings.HasPrefix(packageResult.PackagePath, os.TempDir()) {
			if err := os.RemoveAll(packageResult.PackagePath); err != nil {
				log.Printf("failed to remove temporary package: %s : %s", packageResult.PackagePath, err)
			}
		}

		pa.console.StopSpinner(ctx, stepMessage, input.GetStepResultFormat(err))
		if err != nil {
			return nil, err
		}

		publishResults[svc.Name] = publishResult

		// report publish outputs
		pa.console.MessageUxItem(ctx, publishResult)
	}

	if pa.formatter.Kind() == output.JsonFormat {
		publishResult := PublishResult{
			Timestamp: time.Now(),
			Services:  publishResults,
		}

		if fmtErr := pa.formatter.Format(publishResult, pa.writer, nil); fmtErr != nil {
			return nil, fmt.Errorf("publish result could not be displayed: %w", fmtErr)
		}
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: fmt.Sprintf("Your application was published to the container registry in %s.",
				ux.DurationAsText(since(startTime))),
		},
	}, nil
}

func GetCmdPublishHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription(
		"Publish a service to a container registry.",
		[]string{
			formatHelpNote("Supports Container App services only."),
			formatHelpNote(
				//nolint:lll
				"Target registry set by AZURE_CONTAINER_REGISTRY_ENDPOINT environment variable or docker.registry in azure.yaml."),
		})
}

func GetCmdPublishHelpFooter(*cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		"Publish all services in the current project.": output.WithHighLightFormat(
			"azd publish --all",
		),
		"Publish the service named 'api'.": output.WithHighLightFormat(
			"azd publish api",
		),
		"Publish the service named 'api' with image name 'app/api' and tag 'prod'.": output.WithHighLightFormat(
			"azd publish api --image app/api --tag prod",
		),
		"Publish the service named 'api' from a previously generated package.": output.WithHighLightFormat(
			"azd publish api --from-package <image-tag>",
		),
	})
}
