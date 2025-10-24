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
	"path/filepath"
	"slices"
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
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type PublishFlags struct {
	ServiceName string
	All         bool
	To          string
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
		"Publishes all services that are listed in "+azdcontext.ProjectFileName,
	)

	local.StringVar(
		&f.To,
		"to",
		"",
		"The target container image in the form '[registry/]repository[:tag]' to publish to.",
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
	serviceLocator ioc.ServiceLocator,
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
		serviceLocator:      serviceLocator,
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
	serviceLocator      ioc.ServiceLocator
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

	// Validate that --to requires a specific service
	if pa.flags.All && pa.flags.To != "" {
		return nil, errors.New(
			"'--to' cannot be specified when '--all' is set. Specify a specific service by passing a <service>")
	}

	if targetServiceName == "" && pa.flags.To != "" {
		return nil, errors.New(
			"'--to' cannot be specified when publishing all services. Specify a specific service by passing a <service>",
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

	if pa.flags.FromPackage != "" {
		if parsedImage, err := docker.ParseContainerImage(pa.flags.FromPackage); err == nil && parsedImage.Registry != "" {
			return nil, fmt.Errorf(
				"'%s' is already a remote image. Use '--to' flag to specify target", pa.flags.FromPackage)
		}
	}

	// Create publish options from flags
	publishOptions := &project.PublishOptions{
		Image: pa.flags.To,
	}

	if err := pa.projectManager.Initialize(ctx, pa.projectConfig); err != nil {
		return nil, err
	}

	if err := pa.projectManager.EnsureServiceTargetTools(ctx, pa.projectConfig, func(svc *project.ServiceConfig) bool {
		return targetServiceName == "" || svc.Name == targetServiceName
	}); err != nil {
		return nil, err
	}

	// Command title
	pa.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: "Publishing services (azd publish)",
	})

	startTime := time.Now()

	stableServices, err := pa.importManager.ServiceStable(ctx, pa.projectConfig)
	if err != nil {
		return nil, err
	}

	projectEventArgs := project.ProjectLifecycleEventArgs{
		Project: pa.projectConfig,
	}

	publishResults := map[string]*project.ServicePublishResult{}

	err = pa.projectConfig.Invoke(ctx, project.ProjectEventPublish, projectEventArgs, func() error {
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

			if !pa.supportsPublish(ctx, svc) {
				pa.console.StopSpinner(ctx, stepMessage, input.StepSkipped)

				var message string
				if svc.Host == project.DotNetContainerAppTarget {
					message = "'publish' does not currently support Aspire projects"
				} else {
					message = fmt.Sprintf(
						"'publish' only supports '%s' and '%s' services, but '%s' has host type '%s'",
						project.ContainerAppTarget, project.AksTarget, svc.Name, svc.Host)
				}

				pa.console.MessageUxItem(ctx, &ux.WarningMessage{
					Description: message,
				})
				continue
			}

			// Initialize service context for tracking artifacts across operations
			serviceContext := &project.ServiceContext{}

			if pa.flags.FromPackage != "" {
				// --from-package set, skip packaging and create package artifact
				err = serviceContext.Package.Add(&project.Artifact{
					Kind:         determineArtifactKind(pa.flags.FromPackage),
					Location:     pa.flags.FromPackage,
					LocationKind: project.LocationKindLocal,
				})

				if err != nil {
					pa.console.StopSpinner(ctx, stepMessage, input.StepFailed)
					return err
				}
			} else {
				//  --from-package not set, automatically package the application
				packageResult, err := async.RunWithProgress(
					func(packageProgress project.ServiceProgress) {
						progressMessage := fmt.Sprintf("Packaging service %s (%s)", svc.Name, packageProgress.Message)
						pa.console.ShowSpinner(ctx, progressMessage, input.Step)
					},
					func(progress *async.Progress[project.ServiceProgress]) (*project.ServicePackageResult, error) {
						return pa.serviceManager.Package(ctx, svc, serviceContext, progress, nil)
					},
				)

				if err != nil {
					pa.console.StopSpinner(ctx, stepMessage, input.StepFailed)
					return err
				}

				// Append package artifacts
				if err := serviceContext.Package.Add(packageResult.Artifacts...); err != nil {
					pa.console.StopSpinner(ctx, stepMessage, input.StepFailed)
					return err
				}
			}

			publishResult, err := async.RunWithProgress(
				func(publishProgress project.ServiceProgress) {
					progressMessage := fmt.Sprintf("Publishing service %s (%s)", svc.Name, publishProgress.Message)
					pa.console.ShowSpinner(ctx, progressMessage, input.Step)
				},
				func(progress *async.Progress[project.ServiceProgress]) (*project.ServicePublishResult, error) {
					return pa.serviceManager.Publish(ctx, svc, serviceContext, progress, publishOptions)
				},
			)

			if err != nil {
				pa.console.StopSpinner(ctx, stepMessage, input.StepFailed)
				return err
			}

			if err := serviceContext.Publish.Add(publishResult.Artifacts...); err != nil {
				pa.console.StopSpinner(ctx, stepMessage, input.StepFailed)
				return err
			}

			// clean up for packages automatically created in temp dir
			if pa.flags.FromPackage == "" {
				for _, artifact := range serviceContext.Package {
					if strings.HasPrefix(artifact.Location, os.TempDir()) {
						if err := os.RemoveAll(artifact.Location); err != nil {
							log.Printf("failed to remove temporary package: %s : %s", artifact.Location, err)
						}
					}
				}
			}

			pa.console.StopSpinner(ctx, stepMessage, input.GetStepResultFormat(err))

			publishResults[svc.Name] = publishResult
			pa.console.MessageUxItem(ctx, publishResult.Artifacts)
		}

		return nil
	})

	if err != nil {
		return nil, err
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
			Header: fmt.Sprintf("Your application was published in %s.",
				ux.DurationAsText(since(startTime))),
		},
	}, nil
}

// supportsPublish checks if the service host supports publishing.
func (pa *PublishAction) supportsPublish(ctx context.Context, serviceConfig *project.ServiceConfig) bool {
	// Built-in container targets support publish
	if serviceConfig.Host.RequiresContainer() {
		return true
	}

	// Check if this is a built-in target
	if slices.Contains(project.BuiltInServiceTargetKinds(), serviceConfig.Host) {
		// Built-in non-container targets do not support publish
		return false
	}

	// For extension-provided targets, check if they are registered
	var target project.ServiceTarget
	if err := pa.serviceLocator.ResolveNamed(string(serviceConfig.Host), &target); err == nil {
		return true
	}

	return false
}

// determineArtifactKind identifies the type of artifact based on the fromPackage value.
// It can be one of three types:
// 1. Archive (zip file) - checks if it exists and matches popular archive extensions
// 2. Directory - checks if it is an existing directory (absolute or relative)
// 3. Container - otherwise, it's likely a local container reference
func determineArtifactKind(fromPackage string) project.ArtifactKind {
	// Check if it's an existing file with archive extension
	if info, err := os.Stat(fromPackage); err == nil && !info.IsDir() {
		ext := strings.ToLower(filepath.Ext(fromPackage))
		switch ext {
		case ".zip", ".tar", ".tar.gz", ".tgz", ".tar.bz2", ".tbz2", ".tar.xz", ".txz":
			return project.ArtifactKindArchive
		}
	}

	// Check if it's an existing directory
	if info, err := os.Stat(fromPackage); err == nil && info.IsDir() {
		return project.ArtifactKindDirectory
	}

	// Otherwise, assume it's a container reference
	return project.ArtifactKindContainer
}

func GetCmdPublishHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription(
		"Publish a service to a container registry.",
		[]string{
			formatHelpNote("Supports Container App services only."),
			formatHelpNote(
				//nolint:lll
				"Target registry set by AZURE_CONTAINER_REGISTRY_ENDPOINT environment variable, docker.registry in azure.yaml, or '--to' flag.",
			),
			formatHelpNote(
				//nolint:lll
				"Use '--from-package' to publish an existing container image, otherwise azd automatically packages the container image before publishing.",
			),
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
		"Publish the service named 'api' with custom image name and tag.": output.WithHighLightFormat(
			"azd publish api --to app/api:prod",
		),
		"Publish the service named 'api' from a previously generated package.": output.WithHighLightFormat(
			"azd publish api --from-package <image-tag>",
		),
	})
}
